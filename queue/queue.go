package queue

import (
	"errors"
)

var (
	QueueErrNil          = errors.New("Queue fail(nil)")
	QueueErrEnqueueFull  = errors.New("Enqueue fail(empty)")
	QueueErrDequeueEmpty = errors.New("Dequeue fail(full)")
)

type Queue[T any] struct {
	ch chan T
}

func New[T any](capacity uint32) (*Queue[T], error) {
	ch := make(chan T, capacity)

	return &Queue[T]{
		ch: ch,
	}, nil
}

func (q *Queue[T]) Enqueue(data T) error {
	if q == nil {
		return QueueErrNil
	}

	select {
	case q.ch <- data:
		return nil
	default:
		return QueueErrEnqueueFull
	}
}

func (q *Queue[T]) Dequeue() (data T, err error) {
	if q == nil {
		return data, QueueErrNil
	}

	select {
	case data = <-q.ch:
		return data, nil
	default:
		return data, QueueErrDequeueEmpty
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
