package timeout

import (
	"errors"
	"net/http"
	"time"
)

type responseWriter struct {
	responseWriter      http.ResponseWriter
	timer               StartStopper
	status              int
	writeHeaderTimedOut bool
}

type writeResult struct {
	n   int
	err error
}

// Put a timeout on each WriteHeader and Write call on the
// ResponseWriter. The timeout gets reset for every WriteHeader / Write
// call.
func NewResponseWriter(rw http.ResponseWriter, ti time.Duration) http.ResponseWriter {
	return &responseWriter{
		responseWriter: rw,
		timer:          NewStartStopper(ti),
	}
}

func (tw *responseWriter) Write(p []byte) (int, error) {
	if tw.status == 0 {
		tw.WriteHeader(http.StatusOK)
	}

	if tw.writeHeaderTimedOut {
		return 0, errors.New("responseWriter: Write called after WriteHeader timed out")
	}

	tw.timer.Start()

	writeChan := make(chan writeResult)
	go func() {
		n, err := tw.responseWriter.Write(p)
		writeChan <- writeResult{n: n, err: err}
	}()

	select {
	case wr := <-writeChan:
		tw.timer.Stop()
		return wr.n, wr.err
	case <-tw.timer.Chan():
		go func() {
			<-writeChan // allow writer goroutine to finish
		}()
		return 0, errors.New("responseWriter: Write timeout")
	}
}

func (tw *responseWriter) WriteHeader(status int) {
	if tw.status != 0 {
		return
	}

	tw.status = status
	tw.timer.Start()

	writeHeaderChan := make(chan struct{})
	go func() {
		tw.responseWriter.WriteHeader(status)
		writeHeaderChan <- struct{}{}
	}()

	select {
	case <-writeHeaderChan:
		tw.timer.Stop()
	case <-tw.timer.Chan():
		go func() {
			<-writeHeaderChan
		}()
		tw.writeHeaderTimedOut = true
	}
}

func (tw *responseWriter) Header() http.Header {
	return tw.responseWriter.Header()
}
