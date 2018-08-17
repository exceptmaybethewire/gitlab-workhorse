package proxy

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	opentracing "github.com/opentracing/opentracing-go"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/badgateway"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/helper"
)

var (
	defaultTarget = helper.URLMustParse("http://localhost")
)

type Proxy struct {
	Version                string
	reverseProxy           *httputil.ReverseProxy
	AllowResponseBuffering bool
}

func NewProxy(myURL *url.URL, version string, roundTripper *badgateway.RoundTripper) *Proxy {
	p := Proxy{Version: version, AllowResponseBuffering: true}

	if myURL == nil {
		myURL = defaultTarget
	}

	u := *myURL // Make a copy of p.URL
	u.Path = ""
	p.reverseProxy = httputil.NewSingleHostReverseProxy(&u)
	p.reverseProxy.Transport = roundTripper
	return &p
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Clone request
	req := *r
	req.Header = helper.HeaderClone(r.Header)

	ctx := r.Context()
	if span := opentracing.SpanFromContext(ctx); span != nil {
		// Transmit the span's TraceContext as HTTP headers on our
		// outbound request.
		opentracing.GlobalTracer().Inject(
			span.Context(),
			opentracing.HTTPHeaders,
			opentracing.HTTPHeadersCarrier(req.Header))
	}

	// Set Workhorse version
	req.Header.Set("Gitlab-Workhorse", p.Version)
	req.Header.Set("Gitlab-Workhorse-Proxy-Start", fmt.Sprintf("%d", time.Now().UnixNano()))

	if p.AllowResponseBuffering {
		helper.AllowResponseBuffering(w)
	}

	p.reverseProxy.ServeHTTP(w, &req)
}
