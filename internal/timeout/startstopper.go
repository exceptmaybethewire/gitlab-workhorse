package timeout

import (
	"time"
)

// Not safe for concurrent use. Allowed invocation patterns:
//
// (1) Start() ; Stop() ; Start() ...
//
// (2) Start() ; <- Chan() ; Start() ...
type StartStopper interface {
	Start()
	Stop()
	Chan() <-chan time.Time
}

type startStopper struct {
	timeout time.Duration
	timer   *time.Timer
}

// Returns a new StartStopper in stopped state.
func NewStartStopper(timeout time.Duration) StartStopper {
	ss := &startStopper{
		timeout: timeout,
		timer:   time.NewTimer(timeout),
	}
	ss.Stop()
	return ss
}

func (ss *startStopper) Start() {
	ss.timer.Reset(ss.timeout)
}

func (ss *startStopper) Stop() {
	if !ss.timer.Stop() {
		<-ss.timer.C
	}
}

func (ss *startStopper) Chan() <-chan time.Time {
	return ss.timer.C
}
