package queueing

import (
	"testing"
	"time"
)

func TestNormalQueueing(t *testing.T) {
	q := NewQueue(2, 1)
	err1 := q.Acquire(time.Microsecond)
	if err1 != nil {
		t.Fatal("we should acquire a new slot")
	}

	err2 := q.Acquire(time.Microsecond)
	if err2 != nil {
		t.Fatal("we should acquire a new slot")
	}

	err3 := q.Acquire(time.Microsecond)
	if err3 != ErrQueueingTimedout {
		t.Fatal("we should timeout")
	}

	q.Release()

	err4 := q.Acquire(time.Microsecond)
	if err4 != nil {
		t.Fatal("we should acquire a new slot")
	}
}

func TestQueueLimit(t *testing.T) {
	q := NewQueue(1, 0)
	err1 := q.Acquire(time.Microsecond)
	if err1 != nil {
		t.Fatal("we should acquire a new slot")
	}

	err2 := q.Acquire(time.Microsecond)
	if err2 != ErrTooManyRequests {
		t.Fatal("we should fail because of not enough slots in queue")
	}
}

func TestQueueProcessing(t *testing.T) {
	q := NewQueue(1, 1)
	err1 := q.Acquire(time.Microsecond)
	if err1 != nil {
		t.Fatal("we should acquire a new slot")
	}

	go func() {
		time.Sleep(50 * time.Microsecond)
		q.Release()
	}()

	err2 := q.Acquire(time.Second)
	if err2 != nil {
		t.Fatal("we should acquire slot after the previous one finished")
	}
}
