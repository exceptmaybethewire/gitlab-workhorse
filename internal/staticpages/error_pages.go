package staticpages

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"path/filepath"

	"github.com/prometheus/client_golang/prometheus"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/helper"
)

var (
	staticErrorResponses = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gitlab_workhorse_static_error_responses",
			Help: "How many HTTP responses have been changed to a static error page, by HTTP status code.",
		},
		[]string{"code"},
	)
)

func init() {
	prometheus.MustRegister(staticErrorResponses)
}

type errorPageResponseWriter struct {
	rw       http.ResponseWriter
	status   int
	hijacked bool
	path     string
}

func (s *errorPageResponseWriter) Header() http.Header {
	return s.rw.Header()
}

func (s *errorPageResponseWriter) Write(data []byte) (n int, err error) {
	if s.status == 0 {
		s.WriteHeader(http.StatusOK)
	}
	if s.hijacked {
		return 0, nil
	}
	return s.rw.Write(data)
}

func (s *errorPageResponseWriter) WriteHeader(status int) {
	if s.status != 0 {
		return
	}

	s.status = status

	if 400 <= s.status && s.status <= 599 &&
		s.rw.Header().Get("X-GitLab-Custom-Error") == "" &&
		s.rw.Header().Get("Content-Type") != "application/json" {
		errorPageFile := filepath.Join(s.path, fmt.Sprintf("%d.html", s.status))

		// check if custom error page exists, serve this page instead
		if data, err := ioutil.ReadFile(errorPageFile); err == nil {
			s.hijacked = true
			staticErrorResponses.WithLabelValues(fmt.Sprintf("%d", s.status)).Inc()

			helper.SetNoCacheHeaders(s.rw.Header())
			s.rw.Header().Set("Content-Type", "text/html; charset=utf-8")
			s.rw.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
			s.rw.Header().Del("Transfer-Encoding")
			s.rw.WriteHeader(s.status)
			s.rw.Write(data)
			return
		}
	}

	s.rw.WriteHeader(status)
}

func (s *errorPageResponseWriter) Flush() {
	s.WriteHeader(http.StatusOK)
}

func (st *Static) ErrorPagesUnless(disabled bool, handler http.Handler) http.Handler {
	if disabled {
		return handler
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rw := errorPageResponseWriter{
			rw:   w,
			path: st.DocumentRoot,
		}
		defer rw.Flush()
		handler.ServeHTTP(&rw, r)
	})
}
