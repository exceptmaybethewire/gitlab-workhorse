package timeout

import (
	"net/http"
	"time"
)

type responseWriter struct {
	http.ResponseWriter
	timer  StartStopper
	status int
}

type writeResult struct {
	n   int
	err error
}

type timeoutPanic string

// Put a timeout on each WriteHeader and Write call on the
// ResponseWriter. The timeout gets reset for every WriteHeader / Write
// call. Panics when the timeout is exceeded.
func NewResponseWriter(rw http.ResponseWriter, ti time.Duration) http.ResponseWriter {
	return &responseWriter{
		ResponseWriter: rw,
		timer:          NewStartStopper(ti),
	}
}

func (tw *responseWriter) Write(p []byte) (int, error) {
	if tw.status == 0 {
		tw.WriteHeader(http.StatusOK)
	}

	tw.timer.Start()

	writeChan := make(chan writeResult, 1)
	go func() {
		n, err := tw.ResponseWriter.Write(p)
		writeChan <- writeResult{n: n, err: err}
	}()

	select {
	case wr := <-writeChan:
		tw.timer.Stop()
		return wr.n, wr.err
	case <-tw.timer.Chan():
		panic(timeoutPanic("responseWriter: Write() timeout"))
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
		tw.ResponseWriter.WriteHeader(status)
		close(writeHeaderChan)
	}()

	select {
	case <-writeHeaderChan:
		tw.timer.Stop()
	case <-tw.timer.Chan():
		panic(timeoutPanic("responseWriter: WriteHeader() timeout"))
	}
}
