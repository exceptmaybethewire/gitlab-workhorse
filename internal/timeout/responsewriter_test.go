package timeout

import (
	"net/http"
	"testing"
	"time"
)

func TestResponseWriterTimeouts(t *testing.T) {
	var fast, slow, timeout time.Duration
	timeout = 10 * time.Millisecond
	fast = timeout / 2
	slow = timeout * 2

	writeData := []byte("foobar")

	testCases := []struct {
		success bool
		delays  []time.Duration
	}{
		{true, []time.Duration{fast}},
		{true, []time.Duration{fast, fast, fast}},
		{true, []time.Duration{slow}}, // there is no way to detect a single WriteHeader failure
		{false, []time.Duration{slow, fast, fast}},
		{false, []time.Duration{fast, slow, fast}},
	}

	for _, tc := range testCases {
		w := NewResponseWriter(&slowWriter{delays: tc.delays}, timeout)
		seenErrors := []error{}
		w.WriteHeader(200)
		for range tc.delays[1:] {
			if _, err := w.Write(writeData); err != nil {
				seenErrors = append(seenErrors, err)
			}
		}
		if tc.success && len(seenErrors) > 0 {
			t.Errorf("case %v: no errors expected but received %v", tc, seenErrors[0])
		}
		if !tc.success && len(seenErrors) == 0 {
			t.Errorf("case %v: error expected, none received", tc)
		}
	}
}

type slowWriter struct {
	delays []time.Duration
	i      int
}

func (s *slowWriter) Write(p []byte) (int, error) {
	s.performNextSleep()
	return len(p), nil
}

func (s *slowWriter) WriteHeader(int) {
	s.performNextSleep()
}

func (s *slowWriter) performNextSleep() {
	d := s.delays[s.i]
	s.i++
	time.Sleep(d)
}

func (s *slowWriter) Header() http.Header {
	return nil
}
