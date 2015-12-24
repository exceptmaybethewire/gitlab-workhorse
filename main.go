/*
gitlab-workhorse handles slow requests for GitLab

This HTTP server can service 'git clone', 'git push' etc. commands
from Git clients that use the 'smart' Git HTTP protocol (git-upload-pack
and git-receive-pack). It is intended to be deployed behind NGINX
(for request routing and SSL termination) with access to a GitLab
backend (for authentication and authorization) and local disk access
to Git repositories managed by GitLab. In GitLab, this role was previously
performed by gitlab-grack.

In this file we start the web server and hand off to the upstream type.
*/
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	_ "net/http/pprof"
	"os"
	"regexp"
	"syscall"
	"time"
)

// Current version of GitLab Workhorse
var Version = "(unknown version)" // Set at build time in the Makefile

var printVersion = flag.Bool("version", false, "Print version and exit")
var listenAddr = flag.String("listenAddr", "localhost:8181", "Listen address for HTTP server")
var listenNetwork = flag.String("listenNetwork", "tcp", "Listen 'network' (tcp, tcp4, tcp6, unix)")
var listenUmask = flag.Int("listenUmask", 022, "Umask for Unix socket, default: 022")
var authBackend = flag.String("authBackend", "http://localhost:8080", "Authentication/authorization backend")
var authSocket = flag.String("authSocket", "", "Optional: Unix domain socket to dial authBackend at")
var pprofListenAddr = flag.String("pprofListenAddr", "", "pprof listening address, e.g. 'localhost:6060'")
var documentRoot = flag.String("documentRoot", "public", "Path to static files content")
var responseHeadersTimeout = flag.Duration("proxyHeadersTimeout", time.Minute, "How long to wait for response headers when proxying the request")
var developmentMode = flag.Bool("developmentMode", false, "Allow to serve assets from Rails app")

type httpRoute struct {
	method     string
	regex      *regexp.Regexp
	handleFunc serviceHandleFunc
}

const projectPattern = `^/[^/]+/[^/]+/`
const gitProjectPattern = `^/[^/]+/[^/]+\.git/`

const apiPattern = `^/api/`

// Handle project IDs in two forms: integer ID and group/project_name
const projectsAPIPattern = `^/api/v3/projects/([^/]+|[^/]+/[^/]+)/`

const ciAPIPattern = `^/ci/api/`

// Routing table
// We match against URI not containing the relativeUrlRoot:
// see upstream.ServeHTTP
var httpRoutes = [...]httpRoute{
	// Git Clone
	httpRoute{"GET", regexp.MustCompile(gitProjectPattern + `info/refs\z`), repoPreAuthorizeHandler(handleGetInfoRefs)},
	httpRoute{"POST", regexp.MustCompile(gitProjectPattern + `git-upload-pack\z`), repoPreAuthorizeHandler(contentEncodingHandler(handlePostRPC))},
	httpRoute{"POST", regexp.MustCompile(gitProjectPattern + `git-receive-pack\z`), repoPreAuthorizeHandler(contentEncodingHandler(handlePostRPC))},
	httpRoute{"PUT", regexp.MustCompile(gitProjectPattern + `gitlab-lfs/objects/([0-9a-f]{64})/([0-9]+)\z`), lfsAuthorizeHandler(handleStoreLfsObject)},

	// Repository Archive
	httpRoute{"GET", regexp.MustCompile(projectPattern + `repository/archive\z`), repoPreAuthorizeHandler(handleGetArchive)},
	httpRoute{"GET", regexp.MustCompile(projectPattern + `repository/archive.zip\z`), repoPreAuthorizeHandler(handleGetArchive)},
	httpRoute{"GET", regexp.MustCompile(projectPattern + `repository/archive.tar\z`), repoPreAuthorizeHandler(handleGetArchive)},
	httpRoute{"GET", regexp.MustCompile(projectPattern + `repository/archive.tar.gz\z`), repoPreAuthorizeHandler(handleGetArchive)},
	httpRoute{"GET", regexp.MustCompile(projectPattern + `repository/archive.tar.bz2\z`), repoPreAuthorizeHandler(handleGetArchive)},

	// Repository Archive API
	httpRoute{"GET", regexp.MustCompile(projectsAPIPattern + `repository/archive\z`), repoPreAuthorizeHandler(handleGetArchive)},
	httpRoute{"GET", regexp.MustCompile(projectsAPIPattern + `repository/archive.zip\z`), repoPreAuthorizeHandler(handleGetArchive)},
	httpRoute{"GET", regexp.MustCompile(projectsAPIPattern + `repository/archive.tar\z`), repoPreAuthorizeHandler(handleGetArchive)},
	httpRoute{"GET", regexp.MustCompile(projectsAPIPattern + `repository/archive.tar.gz\z`), repoPreAuthorizeHandler(handleGetArchive)},
	httpRoute{"GET", regexp.MustCompile(projectsAPIPattern + `repository/archive.tar.bz2\z`), repoPreAuthorizeHandler(handleGetArchive)},

	// CI Artifacts API
	httpRoute{"POST", regexp.MustCompile(ciAPIPattern + `v1/builds/[0-9]+/artifacts\z`), artifactsAuthorizeHandler(contentEncodingHandler(handleFileUploads))},

	// Explicitly proxy API requests
	httpRoute{"", regexp.MustCompile(apiPattern), proxyRequest},
	httpRoute{"", regexp.MustCompile(ciAPIPattern), proxyRequest},

	// Serve assets
	httpRoute{"", regexp.MustCompile(`^/assets/`),
		handleServeFile(documentRoot, CacheExpireMax,
			handleDevelopmentMode(developmentMode,
				handleDeployPage(documentRoot,
					handleRailsError(documentRoot,
						proxyRequest,
					),
				),
			),
		),
	},

	// Serve static files or forward the requests
	httpRoute{"", nil,
		handleServeFile(documentRoot, CacheDisabled,
			handleDeployPage(documentRoot,
				handleRailsError(documentRoot,
					proxyRequest,
				),
			),
		),
	},
}

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage of %s:\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "\n  %s [OPTIONS]\n\nOptions:\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()

	version := fmt.Sprintf("gitlab-workhorse %s", Version)
	if *printVersion {
		fmt.Println(version)
		os.Exit(0)
	}

	log.Printf("Starting %s", version)

	// Good housekeeping for Unix sockets: unlink before binding
	if *listenNetwork == "unix" {
		if err := os.Remove(*listenAddr); err != nil && !os.IsNotExist(err) {
			log.Fatal(err)
		}
	}

	// Change the umask only around net.Listen()
	oldUmask := syscall.Umask(*listenUmask)
	listener, err := net.Listen(*listenNetwork, *listenAddr)
	syscall.Umask(oldUmask)
	if err != nil {
		log.Fatal(err)
	}

	// Create Proxy Transport
	authTransport := http.DefaultTransport
	if *authSocket != "" {
		dialer := &net.Dialer{
			// The values below are taken from http.DefaultTransport
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}
		authTransport = &http.Transport{
			Dial: func(_, _ string) (net.Conn, error) {
				return dialer.Dial("unix", *authSocket)
			},
			ResponseHeaderTimeout: *responseHeadersTimeout,
		}
	}
	proxyTransport := &proxyRoundTripper{transport: authTransport}

	// The profiler will only be activated by HTTP requests. HTTP
	// requests can only reach the profiler if we start a listener. So by
	// having no profiler HTTP listener by default, the profiler is
	// effectively disabled by default.
	if *pprofListenAddr != "" {
		go func() {
			log.Print(http.ListenAndServe(*pprofListenAddr, nil))
		}()
	}

	upstream := newUpstream(*authBackend, proxyTransport)
	log.Fatal(http.Serve(listener, upstream))
}
