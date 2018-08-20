/*
The upstream type implements http.Handler.

In this file we handle request routing and interaction with the authBackend.
*/

package upstream

import (
	"fmt"
	"net/http"
	"strings"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/badgateway"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/config"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/helper"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/upload"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/urlprefix"

	"github.com/sebest/xff"
)

var (
	DefaultBackend         = helper.URLMustParse("http://localhost:8080")
	requestHeaderBlacklist = []string{
		upload.RewrittenFieldsHeader,
	}
)

type upstream struct {
	config.Config
	URLPrefix    urlprefix.Prefix
	Routes       []routeEntry
	RoundTripper *badgateway.RoundTripper
}

func NewUpstream(cfg config.Config) http.Handler {
	up := upstream{
		Config: cfg,
	}
	if up.Backend == nil {
		up.Backend = DefaultBackend
	}
	up.RoundTripper = badgateway.NewRoundTripper(up.Backend, up.Socket, up.ProxyHeadersTimeout, cfg.DevelopmentMode)
	up.configureURLPrefix()
	up.configureRoutes()
	return &up
}

func (u *upstream) configureURLPrefix() {
	relativeURLRoot := u.Backend.Path
	if !strings.HasSuffix(relativeURLRoot, "/") {
		relativeURLRoot += "/"
	}
	u.URLPrefix = urlprefix.Prefix(relativeURLRoot)
}

func (u *upstream) ServeHTTP(ow http.ResponseWriter, r *http.Request) {
	// Automatic quasi-intelligent X-Forwarded-For parsing
	r.RemoteAddr = xff.GetRemoteAddr(r)

	w := helper.NewStatsCollectingResponseWriter(ow)
	defer w.RequestFinished(r)

	helper.DisableResponseBuffering(w)

	// Drop RequestURI == "*" (FIXME: why?)
	if r.RequestURI == "*" {
		helper.HTTPError(w, r, "Connection upgrade not allowed", http.StatusBadRequest)
		return
	}

	// Disallow connect
	if r.Method == "CONNECT" {
		helper.HTTPError(w, r, "CONNECT not allowed", http.StatusBadRequest)
		return
	}

	// Check URL Root
	URIPath := urlprefix.CleanURIPath(r.URL.Path)
	prefix := u.URLPrefix
	if !prefix.Match(URIPath) {
		helper.HTTPError(w, r, fmt.Sprintf("Not found %q", URIPath), http.StatusNotFound)
		return
	}

	// Look for a matching route
	var route *routeEntry
	for _, ro := range u.Routes {
		if ro.isMatch(prefix.Strip(URIPath), r) {
			route = &ro
			break
		}
	}

	if route == nil {
		// The protocol spec in git/Documentation/technical/http-protocol.txt
		// says we must return 403 if no matching service is found.
		helper.HTTPError(w, r, "Forbidden", http.StatusForbidden)
		return
	}

	for _, h := range requestHeaderBlacklist {
		r.Header.Del(h)
	}

	route.handler.ServeHTTP(w, r)
}
