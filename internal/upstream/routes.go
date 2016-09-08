package upstream

import (
	"net/http"
	"regexp"

	apipkg "gitlab.com/gitlab-org/gitlab-workhorse/internal/api"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/artifacts"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/git"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/lfs"
	proxypkg "gitlab.com/gitlab-org/gitlab-workhorse/internal/proxy"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/requestbuffer"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/senddata"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/sendfile"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/staticpages"
)

type route struct {
	method  string
	regex   *regexp.Regexp
	handler http.Handler
}

const projectPattern = `^/[^/]+/[^/]+/`
const gitProjectPattern = `^/[^/]+/[^/]+\.git/`

const apiPattern = `^/api/`

// A project ID in an API request is either a number or two strings 'namespace/project'
const projectsAPIPattern = `^/api/v3/projects/((\d+)|([^/]+/[^/]+))/`
const ciAPIPattern = `^/ci/api/`

// Routing table
// We match against URI not containing the relativeUrlRoot:
// see upstream.ServeHTTP

func (u *Upstream) configureRoutes() {
	api := apipkg.NewAPI(
		u.Backend,
		u.Version,
		u.SecretPath,
		u.RoundTripper,
	)
	static := &staticpages.Static{u.DocumentRoot}
	proxy := senddata.SendData(
		sendfile.SendFile(
			apipkg.Block(
				proxypkg.NewProxy(
					u.Backend,
					u.Version,
					u.RoundTripper,
				))),
		git.SendArchive,
		git.SendBlob,
		git.SendDiff,
		git.SendPatch,
		artifacts.SendEntry,
	)

	if u.RequestBufferSize > 0 {
		proxy = requestbuffer.New(u.RequestBufferSize, proxy)
	}

	u.Routes = []route{
		// Git Clone
		route{"GET", regexp.MustCompile(gitProjectPattern + `info/refs\z`), git.GetInfoRefs(api)},
		route{"POST", regexp.MustCompile(gitProjectPattern + `git-upload-pack\z`), contentEncodingHandler(git.PostRPC(api))},
		route{"POST", regexp.MustCompile(gitProjectPattern + `git-receive-pack\z`), contentEncodingHandler(git.PostRPC(api))},
		route{"PUT", regexp.MustCompile(gitProjectPattern + `gitlab-lfs/objects/([0-9a-f]{64})/([0-9]+)\z`), lfs.PutStore(api, proxy)},

		// CI Artifacts
		route{"POST", regexp.MustCompile(ciAPIPattern + `v1/builds/[0-9]+/artifacts\z`), contentEncodingHandler(artifacts.UploadArtifacts(api, proxy))},

		// Explicitly proxy API requests
		route{"", regexp.MustCompile(apiPattern), proxy},
		route{"", regexp.MustCompile(ciAPIPattern), proxy},

		// Serve assets
		route{"", regexp.MustCompile(`^/assets/`),
			static.ServeExisting(u.URLPrefix, staticpages.CacheExpireMax,
				NotFoundUnless(u.DevelopmentMode,
					proxy,
				),
			),
		},

		// For legacy reasons, user uploads are stored under the document root.
		// To prevent anybody who knows/guesses the URL of a user-uploaded file
		// from downloading it we make sure requests to /uploads/ do _not_ pass
		// through static.ServeExisting.
		route{"", regexp.MustCompile(`^/uploads/`), static.ErrorPagesUnless(u.DevelopmentMode, proxy)},

		// Serve static files or forward the requests
		route{"", nil,
			static.ServeExisting(u.URLPrefix, staticpages.CacheDisabled,
				static.DeployPage(
					static.ErrorPagesUnless(u.DevelopmentMode,
						proxy,
					),
				),
			),
		},
	}
}
