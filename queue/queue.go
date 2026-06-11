package queue

import (
	"errors"
)

var (
	QueueErrInvalidCapa  = errors.New("Queue fail(invalid capacity)")
	QueueErrNil          = errors.New("Queue fail(nil)")
	QueueErrClosed       = errors.New("Queue fail(closed)")
	QueueErrEnqueueFull  = errors.New("Enqueue fail(full)")
	QueueErrDequeueEmpty = errors.New("Dequeue fail(empty)")
)

type Queue[T any] struct {
	ch chan T
}

func New[T any](capacity uint32) (*Queue[T], error) {
	if capacity == 0 {
		return nil, QueueErrInvalidCapa
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
		return QueueErrNil
	}

	defer func() {
		if r := recover(); r != nil {
			err = QueueErrClosed
		}
	}()

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
	case d, ok := <-q.ch:
		if ok == false {
			return data, QueueErrClosed
		}
		return d, nil
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
