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
	ErrFull       = ErrorType("pipequeue: put full")
)

type ActionType int

const (
	ActionPut ActionType = iota + 1
	ActionPipe
)

type HandlerFunc[T any] func(*Context[T])

type Queue[T any] struct {
	mu       sync.RWMutex
	isClosed bool
	inCh     chan T
	outCh    chan T
	closeSig chan struct{}

	pool         sync.Pool
	handlers     []HandlerFunc[T]
	putHandlers  []HandlerFunc[T]
	pipeHandlers []HandlerFunc[T]
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

func (q *Queue[T]) Put(data T) error {
	if q == nil {
		return ErrNil
	}

	c := q.pool.Get().(*Context[T])
	defer func() {
		c.reset()
		q.pool.Put(c)
	}()

	q.mu.RLock()
	c.handlers = q.putHandlers
	q.mu.RUnlock()

	c.index, c.action, c.data = -1, ActionPut, data
	c.Next()
	return c.err
}

// C는 소비자가 데이터를 수신하는 출력 채널을 반환합니다.
// Put으로 투입된 데이터는 내부 pipe 고루틴을 거쳐 이 채널로 전달되며,
// 큐에서 데이터를 꺼내는 경로는 이 채널이 유일합니다.
//
// [동시성 및 주의사항]
//   - 출력 채널은 버퍼가 없으므로, 이 채널을 수신하는 소비자가 반드시 존재해야 합니다.
//     수신자가 없으면 pipe 고루틴이 송신 지점에서 대기하며 데이터가 소비되지 않습니다.
//   - Close 이후 pipe 고루틴이 종료하면서 이 채널을 닫으므로,
//     select case에서 사용하던 큐는 Close 후 nil로 변경해야 합니다.
func (q *Queue[T]) C() <-chan T {
	if q == nil {
		return nil
	}

	return q.outCh
}

// Use는 Put/Pipe 연산 전후에 실행할 미들웨어를 체인에 등록합니다.
// 여러 번 호출하면 등록한 순서대로 체인에 누적되며, nil 핸들러는 실행 시 건너뜁니다.
//
// 미들웨어는 연산을 중단하거나 취소할 수 없습니다. 자세한 내용은 [Context.Next]를 참고하십시오.
func (q *Queue[T]) Use(handlerFunc ...HandlerFunc[T]) {
	if q == nil {
		return
	}

	q.mu.Lock()
	defer q.mu.Unlock()

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
	if q.isClosed {
		return
	}

	q.handlers = append(q.handlers, handlerFunc...)

	// 슬라이스 배후 배열 공유 방지를 위해 명시적으로 분리된 슬라이스 생성
	q.putHandlers = make([]HandlerFunc[T], len(q.handlers)+1)
	copy(q.putHandlers, q.handlers)
	q.putHandlers[len(q.handlers)] = func(c *Context[T]) {
		c.err = q.put(c.data)
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

func (q *Queue[T]) put(data T) (err error) {
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
