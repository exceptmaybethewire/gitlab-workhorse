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
	"syscall"
	"time"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/config"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/queueing"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/redis"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/secret"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/upstream"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Current version of GitLab Workhorse
var Version = "(unknown version)" // Set at build time in the Makefile

var printVersion = flag.Bool("version", false, "Print version and exit")
var configFile = flag.String("config", "", "TOML file to load config from")
var listenAddr = flag.String("listenAddr", "localhost:8181", "Listen address for HTTP server")
var listenNetwork = flag.String("listenNetwork", "tcp", "Listen 'network' (tcp, tcp4, tcp6, unix)")
var listenUmask = flag.Int("listenUmask", 0, "Umask for Unix socket")
var authBackend = flag.String("authBackend", upstream.DefaultBackend.String(), "Authentication/authorization backend")
var authSocket = flag.String("authSocket", "", "Optional: Unix domain socket to dial authBackend at")
var pprofListenAddr = flag.String("pprofListenAddr", "", "pprof listening address, e.g. 'localhost:6060'")
var documentRoot = flag.String("documentRoot", "public", "Path to static files content")
var proxyHeadersTimeout = flag.Duration("proxyHeadersTimeout", 5*time.Minute, "How long to wait for response headers when proxying the request")
var developmentMode = flag.Bool("developmentMode", false, "Allow to serve assets from Rails app")
var secretPath = flag.String("secretPath", "./.gitlab_workhorse_secret", "File with secret key to authenticate with authBackend")
var apiLimit = flag.Uint("apiLimit", 0, "Number of API requests allowed at single time")
var apiQueueLimit = flag.Uint("apiQueueLimit", 0, "Number of API requests allowed to be queued")
var apiQueueTimeout = flag.Duration("apiQueueDuration", queueing.DefaultTimeout, "Maximum queueing duration of requests")
var logFile = flag.String("logFile", "", "Log file to be used")
var prometheusListenAddr = flag.String("prometheusListenAddr", "", "Prometheus listening address, e.g. ':9100'")

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

	startLogging(*logFile)

	backendURL, err := parseAuthBackend(*authBackend)
	if err != nil {
		log.Fatalf("invalid authBackend: %v", err)
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

	// The profiler will only be activated by HTTP requests. HTTP
	// requests can only reach the profiler if we start a listener. So by
	// having no profiler HTTP listener by default, the profiler is
	// effectively disabled by default.
	if *pprofListenAddr != "" {
		go func() {
			log.Print(http.ListenAndServe(*pprofListenAddr, nil))
		}()
	}

	if *prometheusListenAddr != "" {
		promMux := http.NewServeMux()
		promMux.Handle("/metrics", promhttp.Handler())
		go func() {
			log.Print(http.ListenAndServe(*prometheusListenAddr, promMux))
		}()
	}

	secret.SetPath(*secretPath)
	cfg := config.Config{
		Backend:             backendURL,
		Socket:              *authSocket,
		Version:             Version,
		DocumentRoot:        *documentRoot,
		DevelopmentMode:     *developmentMode,
		ProxyHeadersTimeout: *proxyHeadersTimeout,
		APILimit:            *apiLimit,
		APIQueueLimit:       *apiQueueLimit,
		APIQueueTimeout:     *apiQueueTimeout,
	}

	if *configFile != "" {
		cfgFromFile, err := config.LoadConfig(*configFile)
		if err != nil {
			log.Fatalf("Can not load config file %q: %v", *configFile, err)
		}

		cfg.Redis = cfgFromFile.Redis

		redis.Configure(cfg.Redis, redis.DefaultDialFunc(cfg.Redis))
		go redis.Process(true)
	}

	up := wrapRaven(upstream.NewUpstream(cfg))

	log.Fatal(http.Serve(listener, up))
}
