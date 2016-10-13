package queueing

import (
	"net/http"
	"time"

	"gitlab.com/gitlab-org/gitlab-workhorse/internal/helper"
)

const DefaultTimeout = 30 * time.Second

func QueueRequests(h http.Handler, limit, queueLimit uint, queueTimeout time.Duration) http.Handler {
	if limit == 0 {
		return h
	}
	if queueTimeout == 0 {
		queueTimeout = DefaultTimeout
	}

	queue := NewQueue(limit, queueLimit)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := queue.Acquire(queueTimeout)

		switch err {
		case nil:
			defer queue.Release()
			h.ServeHTTP(w, r)

		case ErrTooManyRequests:
			helper.TooManyRequests(w, r, err)

		case ErrQueueingTimedout:
			helper.ServiceUnavailable(w, r, err)

		default:
			helper.Fail500(w, r, err)
		}

	})
}
