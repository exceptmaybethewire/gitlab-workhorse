package badgateway

import (
	"bytes"
	"context"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"time"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/helper"
)

// defaultDialer is configured to use the values from http.DefaultTransport
var defaultDialer = &net.Dialer{
	Timeout:   30 * time.Second,
	KeepAlive: 30 * time.Second,
	DualStack: true,
}

// Error is a custom error for pretty Sentry 'issues'
type Error struct{ error }

type RoundTripper struct {
	Transport       *http.Transport
	developmentMode bool
}

func TestRoundTripper(backend *url.URL) *RoundTripper {
	return NewRoundTripper(backend, "", 0, true)
}

// NewRoundTripper returns a new RoundTripper instance using the provided values
func NewRoundTripper(backend *url.URL, socket string, proxyHeadersTimeout time.Duration, developmentMode bool) *RoundTripper {
	// Copied from the definition of http.DefaultTransport. We can't literally copy http.DefaultTransport because of its hidden internal state.
	tr := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           defaultDialer.DialContext,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	tr.ResponseHeaderTimeout = proxyHeadersTimeout

	if backend != nil && socket == "" {
		address := mustParseAddress(backend.Host, backend.Scheme)
		tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return defaultDialer.DialContext(ctx, "tcp", address)
		}
	} else if socket != "" {
		tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return defaultDialer.DialContext(ctx, "unix", socket)
		}
	} else {
		panic("backend is nil and socket is empty")
	}

	return &RoundTripper{Transport: tr, developmentMode: developmentMode}
}

func mustParseAddress(address, scheme string) string {
	if scheme == "https" {
		panic("TLS is not supported for backend connections")
	}

	for _, suffix := range []string{"", ":" + scheme} {
		address += suffix
		if host, port, err := net.SplitHostPort(address); err == nil && host != "" && port != "" {
			return host + ":" + port
		}
	}

	panic(fmt.Errorf("could not parse host:port from address %q and scheme %q", address, scheme))
}

func (t *RoundTripper) RoundTrip(r *http.Request) (res *http.Response, err error) {
	start := time.Now()
	res, err = t.Transport.RoundTrip(r)

	// httputil.ReverseProxy translates all errors from this
	// RoundTrip function into 500 errors. But the most likely error
	// is that the Rails app is not responding, in which case users
	// and administrators expect to see a 502 error. To show 502s
	// instead of 500s we catch the RoundTrip error here and inject a
	// 502 response.
	if err != nil {
		helper.LogError(
			r,
			&Error{fmt.Errorf("badgateway: failed after %.fs: %v", time.Since(start).Seconds(), err)},
		)

		message := "GitLab is not responding"
		if t.developmentMode {
			message = err.Error()
		}

		res = &http.Response{
			StatusCode: http.StatusBadGateway,
			Status:     http.StatusText(http.StatusBadGateway),

			Request:    r,
			ProtoMajor: r.ProtoMajor,
			ProtoMinor: r.ProtoMinor,
			Proto:      r.Proto,
			Header:     make(http.Header),
			Trailer:    make(http.Header),
			Body:       ioutil.NopCloser(bytes.NewBufferString(message)),
		}
		res.Header.Set("Content-Type", "text/plain")
		err = nil
	}
	return
}
