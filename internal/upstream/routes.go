package upstream

import (
	"net/http"
	"path"
	"regexp"

	"github.com/gorilla/websocket"

	apipkg "gitlab.com/gitlab-org/gitlab-workhorse/internal/api"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/artifacts"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/builds"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/filestore"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/git"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/helper"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/lfs"
	proxypkg "gitlab.com/gitlab-org/gitlab-workhorse/internal/proxy"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/queueing"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/redis"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/senddata"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/sendfile"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/sendurl"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/staticpages"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/terminal"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/upload"
)

type matcherFunc func(*http.Request) bool

type routeEntry struct {
	method   string
	regex    *regexp.Regexp
	handler  http.Handler
	matchers []matcherFunc
}

const (
	apiPattern        = `^/api/`
	ciAPIPattern      = `^/ci/api/`
	gitProjectPattern = `^/([^/]+/){1,}[^/]+\.git/`
	projectPattern    = `^/([^/]+/){1,}[^/]+/`
)

func compileRegexp(regexpStr string) *regexp.Regexp {
	if len(regexpStr) == 0 {
		return nil
	}

	return regexp.MustCompile(regexpStr)
}

func route(method, regexpStr string, handler http.Handler, matchers ...matcherFunc) routeEntry {
	return routeEntry{
		method:   method,
		regex:    compileRegexp(regexpStr),
		handler:  instrumentRoute(denyWebsocket(handler), method, regexpStr),
		matchers: matchers,
	}
}

func wsRoute(regexpStr string, handler http.Handler, matchers ...matcherFunc) routeEntry {
	return routeEntry{
		method:   "GET",
		regex:    compileRegexp(regexpStr),
		handler:  instrumentRoute(handler, "GET", regexpStr),
		matchers: append(matchers, websocket.IsWebSocketUpgrade),
	}
}

// Creates matcherFuncs for a particular content type.
func isContentType(contentType string) func(*http.Request) bool {
	return func(r *http.Request) bool {
		return helper.IsContentType(contentType, r.Header.Get("Content-Type"))
	}
}

func (ro *routeEntry) isMatch(cleanedPath string, req *http.Request) bool {
	if ro.method != "" && req.Method != ro.method {
		return false
	}

	if ro.regex != nil && !ro.regex.MatchString(cleanedPath) {
		return false
	}

	ok := true
	for _, matcher := range ro.matchers {
		ok = matcher(req)
		if !ok {
			break
		}
	}

	return ok
}

// Routing table
// We match against URI not containing the relativeUrlRoot:
// see upstream.ServeHTTP

func (u *upstream) configureRoutes() {
	api := apipkg.NewAPI(
		u.Backend,
		u.Version,
		u.RoundTripper,
	)
	static := &staticpages.Static{DocumentRoot: u.DocumentRoot}
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
		git.SendSnapshot,
		artifacts.SendEntry,
		sendurl.SendURL,
	)

	uploadPath := path.Join(u.DocumentRoot, "uploads/tmp")
	uploadAccelerateProxy := upload.Accelerate(&upload.SkipRailsAuthorizer{TempPath: uploadPath}, proxy)
	ciAPIProxyQueue := queueing.QueueRequests("ci_api_job_requests", uploadAccelerateProxy, u.APILimit, u.APIQueueLimit, u.APIQueueTimeout)
	ciAPILongPolling := builds.RegisterHandler(ciAPIProxyQueue, redis.WatchKey, u.APICILongPollingDuration)

	u.Routes = []routeEntry{
		// Git Clone
		route("GET", gitProjectPattern+`info/refs\z`, git.GetInfoRefsHandler(api)),
		route("POST", gitProjectPattern+`git-upload-pack\z`, contentEncodingHandler(git.UploadPack(api)), isContentType("application/x-git-upload-pack-request")),
		route("POST", gitProjectPattern+`git-receive-pack\z`, contentEncodingHandler(git.ReceivePack(api)), isContentType("application/x-git-receive-pack-request")),
		route("PUT", gitProjectPattern+`gitlab-lfs/objects/([0-9a-f]{64})/([0-9]+)\z`, lfs.PutStore(api, proxy), isContentType("application/octet-stream")),

		// CI Artifacts
		route("POST", apiPattern+`v4/jobs/[0-9]+/artifacts\z`, contentEncodingHandler(artifacts.UploadArtifacts(api, proxy))),
		route("POST", ciAPIPattern+`v1/builds/[0-9]+/artifacts\z`, contentEncodingHandler(artifacts.UploadArtifacts(api, proxy))),

		// Terminal websocket
		wsRoute(projectPattern+`environments/[0-9]+/terminal.ws\z`, terminal.Handler(api)),
		wsRoute(projectPattern+`-/jobs/[0-9]+/terminal.ws\z`, terminal.Handler(api)),

		// Long poll and limit capacity given to jobs/request and builds/register.json
		route("", apiPattern+`v4/jobs/request\z`, ciAPILongPolling),
		route("", ciAPIPattern+`v1/builds/register.json\z`, ciAPILongPolling),

		// Maven Artifact Repository
		route("PUT", apiPattern+`v4/projects/[0-9]+/packages/maven/`, filestore.BodyUploader(api, proxy, nil)),

		// Explicitly proxy API requests
		route("", apiPattern, proxy),
		route("", ciAPIPattern, proxy),

		// Serve assets
		route(
			"", `^/assets/`,
			static.ServeExisting(
				u.URLPrefix,
				staticpages.CacheExpireMax,
				NotFoundUnless(u.DevelopmentMode, proxy),
			),
		),

		// Uploads
		route("POST", projectPattern+`uploads\z`, upload.Accelerate(api, proxy)),

		// For legacy reasons, user uploads are stored under the document root.
		// To prevent anybody who knows/guesses the URL of a user-uploaded file
		// from downloading it we make sure requests to /uploads/ do _not_ pass
		// through static.ServeExisting.
		route("", `^/uploads/`, static.ErrorPagesUnless(u.DevelopmentMode, proxy)),

		// Serve static files or forward the requests
		route(
			"", "",
			static.ServeExisting(
				u.URLPrefix,
				staticpages.CacheDisabled,
				static.DeployPage(static.ErrorPagesUnless(u.DevelopmentMode, uploadAccelerateProxy)),
			),
		),
	}
}

func denyWebsocket(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if websocket.IsWebSocketUpgrade(r) {
			helper.HTTPError(w, r, "websocket upgrade not allowed", http.StatusBadRequest)
			return
		}

		next.ServeHTTP(w, r)
	})
}
