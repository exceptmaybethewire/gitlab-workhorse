package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net/http"
)

type proxyRoundTripper struct {
	transport http.RoundTripper
}

func (p *proxyRoundTripper) RoundTrip(r *http.Request) (res *http.Response, err error) {
	res, err = p.transport.RoundTrip(r)

	// httputil.ReverseProxy translates all errors from this
	// RoundTrip function into 500 errors. But the most likely error
	// is that the Rails app is not responding, in which case users
	// and administrators expect to see a 502 error. To show 502s
	// instead of 500s we catch the RoundTrip error here and inject a
	// 502 response.
	if err != nil {
		logError(fmt.Errorf("proxyRoundTripper: %s %q failed with: %q", r.Method, r.RequestURI, err))

		res = &http.Response{
			StatusCode: http.StatusBadGateway,
			Status:     http.StatusText(http.StatusBadGateway),

			Request:    r,
			ProtoMajor: r.ProtoMajor,
			ProtoMinor: r.ProtoMinor,
			Proto:      r.Proto,
			Header:     make(http.Header),
			Trailer:    make(http.Header),
			Body:       ioutil.NopCloser(bytes.NewBufferString(err.Error())),
		}
		res.Header.Set("Content-Type", "text/plain")
		err = nil
	}
	return
}

func headerClone(h http.Header) http.Header {
	h2 := make(http.Header, len(h))
	for k, vv := range h {
		vv2 := make([]string, len(vv))
		copy(vv2, vv)
		h2[k] = vv2
	}
	return h2
}

func proxyRequest(w http.ResponseWriter, r *gitRequest) {
	// Clone request
	req := *r.Request
	req.Header = headerClone(r.Header)

	// Set Workhorse version
	req.Header.Set("Gitlab-Workhorse", Version)
	rw := newSendFileResponseModifier(w, &req)
	defer rw.Flush()

	r.u.httpProxy.ServeHTTP(rw, &req)
}
