package helper

import (
	"bufio"
	"net"
	"net/http"
	"time"

	log "github.com/sirupsen/logrus"
	logging "gitlab.com/gitlab-org/gitlab-workhorse/internal/log"
)

var (
	accessLogEntry *log.Entry
)

// SetAccessLoggerEntry sets the access logger used in the system
func SetAccessLoggerEntry(logEntry *log.Entry) {
	accessLogEntry = logEntry
}

type LoggingResponseWriter interface {
	http.ResponseWriter

	RequestFinished(r *http.Request)
}

type statsCollectingResponseWriter struct {
	rw          http.ResponseWriter
	status      int
	wroteHeader bool
	written     int64
	started     time.Time
}

type hijackingResponseWriter struct {
	statsCollectingResponseWriter
}

func NewStatsCollectingResponseWriter(rw http.ResponseWriter) LoggingResponseWriter {
	out := statsCollectingResponseWriter{
		rw:      rw,
		started: time.Now(),
	}

	if _, ok := rw.(http.Hijacker); ok {
		return &hijackingResponseWriter{out}
	}

	return &out
}

func (l *hijackingResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	// The only way to get here is through NewStatsCollectingResponseWriter(), which
	// checks that this cast will be valid.
	hijacker := l.rw.(http.Hijacker)
	return hijacker.Hijack()
}

func (l *statsCollectingResponseWriter) Header() http.Header {
	return l.rw.Header()
}

func (l *statsCollectingResponseWriter) Write(data []byte) (n int, err error) {
	if !l.wroteHeader {
		l.WriteHeader(http.StatusOK)
	}
	n, err = l.rw.Write(data)

	l.written += int64(n)
	return n, err
}

func (l *statsCollectingResponseWriter) WriteHeader(status int) {
	if l.wroteHeader {
		return
	}
	l.wroteHeader = true
	l.status = status

	l.rw.WriteHeader(status)
}

func (l *statsCollectingResponseWriter) writeAccessLog(r *http.Request) {
	if accessLogEntry == nil {
		return
	}

	logEntry := accessLogEntry.WithFields(l.accessLogFields(r))
	logging.WrapEntry(r.Context(), logEntry).Info("access")
}

func (l *statsCollectingResponseWriter) accessLogFields(r *http.Request) log.Fields {
	duration := time.Since(l.started)

	return log.Fields{
		"host":       r.Host,
		"remoteAddr": r.RemoteAddr,
		"method":     r.Method,
		"uri":        ScrubURLParams(r.RequestURI),
		"proto":      r.Proto,
		"status":     l.status,
		"written":    l.written,
		"referer":    ScrubURLParams(r.Referer()),
		"userAgent":  r.UserAgent(),
		"duration":   duration.Seconds(),
	}
}

func (l *statsCollectingResponseWriter) RequestFinished(r *http.Request) {
	l.writeAccessLog(r)
}
