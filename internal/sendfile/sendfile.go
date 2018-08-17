/*
The xSendFile middleware transparently sends static files in HTTP responses
via the X-Sendfile mechanism. All that is needed in the Rails code is the
'send_file' method.
*/

package sendfile

import (
	"net/http"
	"regexp"

	"github.com/prometheus/client_golang/prometheus"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/helper"
	"gitlab.com/gitlab-org/gitlab-workhorse/internal/log"
)

const sendFileResponseHeader = "X-Sendfile"

var (
	sendFileRequests = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gitlab_workhorse_sendfile_requests",
			Help: "How many X-Sendfile requests have been processed by gitlab-workhorse, partitioned by sendfile type.",
		},
		[]string{"type"},
	)

	sendFileBytes = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gitlab_workhorse_sendfile_bytes",
			Help: "How many X-Sendfile bytes have been sent by gitlab-workhorse, partitioned by sendfile type.",
		},
		[]string{"type"},
	)

	artifactsSendFile = regexp.MustCompile("builds/[0-9]+/artifacts")
)

type sendFileResponseWriter struct {
	rw       http.ResponseWriter
	status   int
	hijacked bool
	req      *http.Request
}

func init() {
	prometheus.MustRegister(sendFileRequests)
	prometheus.MustRegister(sendFileBytes)
}

func SendFile(h http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		s := &sendFileResponseWriter{
			rw:  rw,
			req: req,
		}
		// Advertise to upstream (Rails) that we support X-Sendfile
		req.Header.Set("X-Sendfile-Type", "X-Sendfile")
		defer s.Flush()
		h.ServeHTTP(s, req)
	})
}

func (s *sendFileResponseWriter) Header() http.Header {
	return s.rw.Header()
}

func (s *sendFileResponseWriter) Write(data []byte) (n int, err error) {
	if s.status == 0 {
		s.WriteHeader(http.StatusOK)
	}
	if s.hijacked {
		return
	}
	return s.rw.Write(data)
}

func (s *sendFileResponseWriter) WriteHeader(status int) {
	if s.status != 0 {
		return
	}

	s.status = status
	if s.status != http.StatusOK {
		s.rw.WriteHeader(s.status)
		return
	}

	if file := s.Header().Get(sendFileResponseHeader); file != "" {
		s.Header().Del(sendFileResponseHeader)
		// Mark this connection as hijacked
		s.hijacked = true

		// Serve the file
		helper.DisableResponseBuffering(s.rw)
		sendFileFromDisk(s.rw, s.req, file)
		return
	}

	s.rw.WriteHeader(s.status)
}

func sendFileFromDisk(w http.ResponseWriter, r *http.Request, file string) {
	log.WithFields(r.Context(), log.Fields{
		"file":   file,
		"method": r.Method,
		"uri":    helper.ScrubURLParams(r.RequestURI),
	}).Print("Send file")

	content, fi, err := helper.OpenFile(file)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer content.Close()

	countSendFileMetrics(fi.Size(), r)

	http.ServeContent(w, r, "", fi.ModTime(), content)
}

func countSendFileMetrics(size int64, r *http.Request) {
	var requestType string
	switch {
	case artifactsSendFile.MatchString(r.RequestURI):
		requestType = "artifacts"
	default:
		requestType = "other"
	}

	sendFileRequests.WithLabelValues(requestType).Inc()
	sendFileBytes.WithLabelValues(requestType).Add(float64(size))
}

func (s *sendFileResponseWriter) Flush() {
	s.WriteHeader(http.StatusOK)
}
