package queue

import (
	"errors"
)

var (
	ErrInvalidCapa  = errors.New("Queue fail(invalid capacity)")
	ErrNil          = errors.New("Queue fail(nil)")
	ErrClosed       = errors.New("Queue fail(closed)")
	ErrEnqueueFull  = errors.New("Enqueue fail(full)")
	ErrDequeueEmpty = errors.New("Dequeue fail(empty)")
)

type Queue[T any] struct {
	ch chan T
}

func New[T any](capacity uint32) (*Queue[T], error) {
	if capacity == 0 {
		return nil, ErrInvalidCapa
	}

	ch := make(chan T, capacity)

	return &Queue[T]{
		ch: ch,
	}, nil
}

func (q *Queue[T]) Close() {
	if q == nil {
		return
	}

	defer func() {
		recover()
	}()

	close(q.ch)
}

func (q *Queue[T]) Enqueue(data T) (err error) {
	if q == nil {
		return ErrNil
	}

	defer func() {
		if r := recover(); r != nil {
			err = ErrClosed
		}
	}()

	select {
	case q.ch <- data:
		return nil
	default:
		return ErrEnqueueFull
	}
}

func (q *Queue[T]) Dequeue() (data T, err error) {
	if q == nil {
		return data, ErrNil
	}

	select {
	case d, ok := <-q.ch:
		if ok == false {
			return data, ErrClosed
		}
		return d, nil
	default:
		return data, ErrDequeueEmpty
	}
}

func (q *Queue[T]) Len() int {
	if q == nil {
		return 0
	}
	return len(q.ch)
}

func (q *Queue[T]) Cap() int {
	if q == nil {
		return 0
	}
	return cap(q.ch)
}
