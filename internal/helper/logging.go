package helper

import (
	"fmt"
	"net/http"
	"time"
)

type LoggingResponseWriter struct {
	rw      http.ResponseWriter
	status  int
	written int64
	started time.Time
}

func NewLoggingResponseWriter(rw http.ResponseWriter) *LoggingResponseWriter {
	return &LoggingResponseWriter{
		rw:      rw,
		started: time.Now(),
	}
}

func (l *LoggingResponseWriter) Header() http.Header {
	return l.rw.Header()
}

func (l *LoggingResponseWriter) Write(data []byte) (n int, err error) {
	if l.status == 0 {
		l.WriteHeader(http.StatusOK)
	}
	n, err = l.rw.Write(data)
	l.written += int64(n)
	return
}

func (l *LoggingResponseWriter) WriteHeader(status int) {
	if l.status != 0 {
		return
	}

	l.status = status
	l.rw.WriteHeader(status)
}

func (l *LoggingResponseWriter) Log(r *http.Request) {
	duration := time.Since(l.started)
	fmt.Printf("%s %s - - [%s] %q %d %d %q %q %f\n",
		r.Host, r.RemoteAddr, l.started,
		fmt.Sprintf("%s %s %s", r.Method, r.RequestURI, r.Proto),
		l.status, l.written, r.Referer(), r.UserAgent(), duration.Seconds(),
	)
}
