package pipequeue

import (
	"sync"
)

type ErrorType string

func (e ErrorType) Error() string {
	return string(e)
}

const (
	ErrInvalidCap = ErrorType("pipequeue: invalid capacity")
	ErrNil        = ErrorType("pipequeue: nil")
	ErrClosed     = ErrorType("pipequeue: closed")
	ErrFull       = ErrorType("pipequeue: enqueue full")
	ErrEmpty      = ErrorType("pipequeue: dequeue empty")
)

type ActionType int

const (
	ActionEnqueue ActionType = iota + 1
	ActionDequeue
	ActionPipe
)

type HandlerFunc[T any] func(*Context[T])

type Queue[T any] struct {
	mu       sync.RWMutex
	isClosed bool
	inCh     chan T
	outCh    chan T
	closeSig chan struct{}

	pool            sync.Pool
	handlers        []HandlerFunc[T]
	enqueueHandlers []HandlerFunc[T]
	dequeueHandlers []HandlerFunc[T]
	pipeHandlers    []HandlerFunc[T]
}

func New[T any](capacity int) (*Queue[T], error) {
	if capacity <= 0 {
		return nil, ErrInvalidCap
	}

	q := &Queue[T]{
		inCh:     make(chan T, capacity),
		outCh:    make(chan T),
		closeSig: make(chan struct{}),
	}

	q.pool.New = func() any {
		return &Context[T]{}
	}

	q.rebuildHandlers()

	go q.pipe()

	return q, nil
}

// Close를 호출하면 PipeQueue 자원이 모두 해제되기 때문에
// select case에서 사용하던 PipeQueue는 nil로 변경해야 함.
func (q *Queue[T]) Close() {
	if q == nil {
		return
	}

	// Lock을 사용해서 채널 close시 race-condition 보호
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.isClosed {
		return
	}

	q.isClosed = true
	close(q.inCh)
	// q.inCh = nil 은 하지 않고, q가 참조 해제되면 GC에서 처리
	close(q.closeSig) // pipe 고루틴을 종료
}

func (q *Queue[T]) Enqueue(data T) error {
	if q == nil {
		return ErrNil
	}

	c := q.pool.Get().(*Context[T])
	defer func() {
		c.reset()
		q.pool.Put(c)
	}()

	q.mu.RLock()
	c.handlers = q.enqueueHandlers
	q.mu.RUnlock()

	c.index, c.action, c.data = -1, ActionEnqueue, data
	c.Next()
	return c.err
}

func (q *Queue[T]) Dequeue() (T, error) {
	var zero T

	if q == nil {
		return zero, ErrNil
	}

	c := q.pool.Get().(*Context[T])
	defer func() {
		c.reset()
		q.pool.Put(c)
	}()

	q.mu.RLock()
	c.handlers = q.dequeueHandlers
	q.mu.RUnlock()

	c.index, c.action = -1, ActionDequeue
	c.Next()
	return c.data, c.err
}

func (q *Queue[T]) C() <-chan T {
	if q == nil {
		return nil
	}

	return q.outCh
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

	q.mu.RLock()
	defer q.mu.RUnlock()

	// close된 채널에서 처리 가능함
	return len(q.inCh)
}

func (q *Queue[T]) Cap() int {
	if q == nil {
		return 0
	}

	q.mu.RLock()
	defer q.mu.RUnlock()

	// close된 채널에서 처리 가능함
	return cap(q.inCh)
}

func (q *Queue[T]) IsFull() bool {
	if q == nil {
		return false
	}

	q.mu.RLock()
	defer q.mu.RUnlock()

	// close된 채널에서 처리 가능함
	return len(q.inCh) == cap(q.inCh)
}

func (q *Queue[T]) IsClosed() bool {
	if q == nil {
		return true
	}

	q.mu.RLock()
	defer q.mu.RUnlock()

	return q.isClosed
}

func (q *Queue[T]) rebuildHandlers(handlerFunc ...HandlerFunc[T]) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.isClosed {
		return
	}

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

	q.pipeHandlers = make([]HandlerFunc[T], len(q.handlers)+1)
	copy(q.pipeHandlers, q.handlers)
	q.pipeHandlers[len(q.handlers)] = func(c *Context[T]) {
		q.write(c.data)
	}
}

func (q *Queue[T]) pipe() {
	if q == nil {
		return
	}
	defer close(q.outCh)

	for {
		select {
		case data, ok := <-q.inCh:
			if !ok {
				return
			}
			c := q.pool.Get().(*Context[T])
			q.mu.RLock()
			c.handlers = q.pipeHandlers
			q.mu.RUnlock()

			c.index, c.action, c.data = -1, ActionPipe, data
			c.Next()

			c.reset()
			q.pool.Put(c)

		case <-q.closeSig: // 고루틴을 종료하면서, outCh를 닫음
			return
		}
	}
}

func (q *Queue[T]) enqueue(data T) (err error) {
	// RLock을 사용해도 채널에서 동기화 가능
	q.mu.RLock()
	defer q.mu.RUnlock()

	if q.isClosed {
		return ErrClosed
	}

	select {
	case q.inCh <- data:
		return nil
	default:
		return ErrFull
	}
}

func (q *Queue[T]) dequeue() (T, error) {
	var zero T

	// RLock을 사용해도 채널에서 동기화 가능
	q.mu.RLock()
	defer q.mu.RUnlock()

	select {
	case data, ok := <-q.inCh:
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

func (q *Queue[T]) write(data T) {
	select {
	case <-q.closeSig:
		return
	default:
	}

	select {
	case q.outCh <- data:
	case <-q.closeSig:
	}
}
