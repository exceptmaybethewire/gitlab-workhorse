package queueing

import (
	"errors"
	"time"
)

type errTooManyRequests struct{ error }
type errQueueingTimedout struct{ error }

var ErrTooManyRequests = &errTooManyRequests{errors.New("too many requests queued")}
var ErrQueueingTimedout = &errQueueingTimedout{errors.New("queueing timedout")}

type Queue struct {
	busyCh    chan struct{}
	waitingCh chan struct{}
}

// NewQueue creates a new queue
// limit specifies number of requests run concurrently
// queueLimit specifies maximum number of requests that can be queued
// if the number of requests is above the limit
func NewQueue(limit, queueLimit uint) *Queue {
	return &Queue{
		busyCh:    make(chan struct{}, limit),
		waitingCh: make(chan struct{}, limit+queueLimit),
	}
}

// Acquire takes one slot from the Queue
// and returns when a request should be processed
// it allows up to (limit) of requests running at a time
// it allows to queue up to (queue-limit) requests
func (s *Queue) Acquire(timeout time.Duration) (err error) {
	// push item to a queue to claim your own slot (non-blocking)
	select {
	case s.waitingCh <- struct{}{}:
		break
	default:
		return ErrTooManyRequests
	}

	defer func() {
		if err != nil {
			<-s.waitingCh
		}
	}()

	// fast path: push item to current processed items (non-blocking)
	select {
	case s.busyCh <- struct{}{}:
		return nil
	default:
		break
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	// push item to current processed items (blocking)
	select {
	case s.busyCh <- struct{}{}:
		return nil

	case <-timer.C:
		return ErrQueueingTimedout
	}
}

// Release marks the finish of processing of requests
// It triggers next request to be processed if it's in queue
func (s *Queue) Release() {
	// dequeue from queue to allow next request to be processed
	<-s.waitingCh
	<-s.busyCh
}
