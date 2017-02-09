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
		{false, []time.Duration{slow}},
		{false, []time.Duration{slow, fast, fast}},
		{false, []time.Duration{fast, slow, fast}},
	}

	for _, tc := range testCases {
		w := NewResponseWriter(&slowWriter{delays: tc.delays}, timeout)
		seenPanics := catchPanic(nil, func() {
			w.WriteHeader(200)
		})
		for range tc.delays[1:] {
			seenPanics = catchPanic(seenPanics, func() {
				if _, err := w.Write(writeData); err != nil {
					t.Fatal(err)
				}
			})
		}
		if tc.success && len(seenPanics) > 0 {
			t.Errorf("case %v: no panics expected but received %v", tc, seenPanics[0])
		}
		if !tc.success && len(seenPanics) == 0 {
			t.Errorf("case %v: panic expected, none happened", tc)
		}
	}
}

func catchPanic(seenPanics []timeoutPanic, cb func()) (result []timeoutPanic) {
	result = append([]timeoutPanic{}, seenPanics...)
	defer func() {
		if p := recover(); p != nil {
			result = append(result, p.(timeoutPanic))
		}
	}()
	cb()
	return result
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
