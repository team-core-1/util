package queue

import (
	"sync"
)

type ErrorType string

func (e ErrorType) Error() string {
	return string(e)
}

const (
	ErrInvalidCap = ErrorType("Queue fail(invalid capacity)")
	ErrNil        = ErrorType("Queue fail(nil)")
	ErrClosed     = ErrorType("Queue fail(closed)")
	ErrFull       = ErrorType("Enqueue fail(full)")
	ErrEmpty      = ErrorType("Dequeue fail(empty)")
)

type ActionType int

const (
	ActionEnqueue ActionType = iota + 1
	ActionDequeue
)

type HandlerFunc[T any] func(*Context[T])

type Queue[T any] struct {
	lock     sync.RWMutex
	isClosed bool
	ch       chan T
	pool     sync.Pool

	handlers        []HandlerFunc[T]
	enqueueHandlers []HandlerFunc[T]
	dequeueHandlers []HandlerFunc[T]
}

func New[T any](capacity int) (*Queue[T], error) {
	if capacity <= 0 {
		return nil, ErrInvalidCap
	}

	q := &Queue[T]{
		ch: make(chan T, capacity),
	}

	q.pool.New = func() any {
		return &Context[T]{}
	}

	q.rebuildHandlers()

	return q, nil
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

func (q *Queue[T]) Enqueue(data T) error {
	if q == nil {
		return ErrNil
	}

	c := q.pool.Get().(*Context[T])
	c.reset()

	q.lock.RLock()
	c.handlers = q.enqueueHandlers
	q.lock.RUnlock()
	c.Action = ActionEnqueue
	c.data = data

	c.index = -1
	c.Next()
	err := c.err

	c.reset()
	q.pool.Put(c)

	return err
}

func (q *Queue[T]) Dequeue() (T, error) {
	var zero T

	if q == nil {
		return zero, ErrNil
	}

	c := q.pool.Get().(*Context[T])
	c.reset()

	q.lock.RLock()
	c.handlers = q.dequeueHandlers
	q.lock.RUnlock()
	c.Action = ActionDequeue

	c.index = -1
	c.Next()
	data, err := c.data, c.err

	c.reset()
	q.pool.Put(c)

	return data, err
}

func (q *Queue[T]) C() <-chan T {
	if q == nil {
		return nil
	}

	return q.ch
}

func (q *Queue[T]) rebuildHandlers(handlerFunc ...HandlerFunc[T]) {
	q.lock.Lock()
	defer q.lock.Unlock()

	q.handlers = append(q.handlers, handlerFunc...)

	// 슬라이스 배후 배열 공유 방지를 위해 명시적으로 분리된 슬라이스 생성
	q.enqueueHandlers = make([]HandlerFunc[T], len(q.handlers)+1)
	copy(q.enqueueHandlers, q.handlers)
	q.enqueueHandlers[len(q.handlers)] = func(c *Context[T]) {
		c.err = q.enqueue(c.data)
	}

	q.dequeueHandlers = make([]HandlerFunc[T], len(q.handlers)+1)
	copy(q.dequeueHandlers, q.handlers)
	q.dequeueHandlers[len(q.handlers)] = func(c *Context[T]) {
		c.data, c.err = q.dequeue()
	}
}

func (q *Queue[T]) Use(handlerFunc ...HandlerFunc[T]) {
	if q == nil {
		return
	}

	q.rebuildHandlers(handlerFunc...)
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

func (q *Queue[T]) enqueue(data T) (err error) {
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

func (q *Queue[T]) dequeue() (T, error) {
	var zero T

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
