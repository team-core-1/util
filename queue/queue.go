package queue

import (
	"errors"
	"sync"
)

var (
	ErrInvalidCap = errors.New("Queue fail(invalid capacity)")
	ErrNil        = errors.New("Queue fail(nil)")
	ErrClosed     = errors.New("Queue fail(closed)")
	ErrFull       = errors.New("Enqueue fail(full)")
	ErrEmpty      = errors.New("Dequeue fail(empty)")
)

type Queue[T any] struct {
	lock     sync.RWMutex
	isClosed bool
	ch       chan T
}

func New[T any](capacity int) (*Queue[T], error) {
	if capacity <= 0 {
		return nil, ErrInvalidCap
	}

	return &Queue[T]{
		ch: make(chan T, capacity),
	}, nil
}

func (q *Queue[T]) Close() {
	if q == nil {
		return
	}

	// Lock을 사용해서 채널 close시 race-condition 보호
	q.lock.Lock()
	defer q.lock.Unlock()

	if q.isClosed {
		return
	}

	q.isClosed = true
	close(q.ch)
	// q.ch = nil 은 하지 않고, q가 참조 해제되면 GC에서 처리
}

func (q *Queue[T]) Enqueue(data T) (err error) {
	if q == nil {
		return ErrNil
	}

	// RLock을 사용해도 채널에서 동기화 가능
	q.lock.RLock()
	defer q.lock.RUnlock()

	if q.isClosed {
		return ErrClosed
	}

	select {
	case q.ch <- data:
		return nil
	default:
		return ErrFull
	}
}

func (q *Queue[T]) Dequeue() (T, error) {
	var zero T

	if q == nil {
		return zero, ErrNil
	}

	// RLock을 사용해도 채널에서 동기화 가능
	q.lock.RLock()
	defer q.lock.RUnlock()

	select {
	case data, ok := <-q.ch:
		// close된 채널에서 처리 가능함
		if !ok {
			return zero, ErrClosed
		}
		return data, nil
	default:
		if q.isClosed {
			return zero, ErrClosed
		}
		return zero, ErrEmpty
	}
}

func (q *Queue[T]) C() <-chan T {
	if q == nil {
		return nil
	}

	return q.ch
}

func (q *Queue[T]) Len() int {
	if q == nil {
		return 0
	}

	q.lock.RLock()
	defer q.lock.RUnlock()

	// close된 채널에서 처리 가능함
	return len(q.ch)
}

func (q *Queue[T]) Cap() int {
	if q == nil {
		return 0
	}

	q.lock.RLock()
	defer q.lock.RUnlock()

	// close된 채널에서 처리 가능함
	return cap(q.ch)
}

func (q *Queue[T]) IsFull() bool {
	if q == nil {
		return false
	}

	q.lock.RLock()
	defer q.lock.RUnlock()

	// close된 채널에서 처리 가능함
	return len(q.ch) == cap(q.ch)
}
