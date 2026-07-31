package pipequeue

import (
	"sync"
	"sync/atomic"
)

type ErrorType string

func (e ErrorType) Error() string {
	return string(e)
}

const (
	ErrInvalidCap = ErrorType("pipequeue: invalid capacity")
	ErrNil        = ErrorType("pipequeue: queue is nil")
	ErrClosed     = ErrorType("pipequeue: queue is closed")
	ErrFull       = ErrorType("pipequeue: queue is full")
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
	closeSig chan struct{}
	inCh     chan T
	outCh    chan T
	len      atomic.Int64

	pool         sync.Pool
	handlers     []HandlerFunc[T]
	putHandlers  []HandlerFunc[T]
	pipeHandlers []HandlerFunc[T]
}

// New는 지정한 용량의 PipeQueue를 생성하고 내부 pipe 고루틴을 시작합니다.
//
// [반드시 Close를 호출하십시오]
// 내부 pipe 고루틴이 큐 자신을 참조하므로, Close 없이 큐 참조만 버리면
// 고루틴이 계속 살아남아 큐와 버퍼에 담긴 데이터까지 GC 대상이 되지 않습니다.
// 사용이 끝나면 반드시 Close를 호출하십시오.
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

// Close는 PipeQueue를 닫고 내부 pipe 고루틴을 종료시킵니다.
// 여러 번 호출해도 안전하며, Close 이후의 Put은 ErrClosed를 반환합니다.
// Close를 호출하면 자원이 모두 해제되므로, select case에서 사용하던 PipeQueue는 nil로 변경해야 합니다.
//
// [남아 있는 데이터는 폐기됩니다]
// Close 시점에 큐에 남아 있던 데이터는 C()로 전달되지 않고 버려집니다.
// Put이 nil(성공)을 반환했더라도 소비 이전에 Close되면 그 데이터는 유실됩니다.
//
// 이는 의도된 동작입니다. 남은 데이터를 끝까지 전달하려면 출력 채널을 비워 줄 수신자가 필요한데,
// 종료 시점에 수신자가 이미 사라졌다면 pipe 고루틴이 송신 지점에서 영원히 대기하며 누수됩니다.
// 종료가 지연되거나 고루틴이 남는 것보다 즉시 종료를 택했습니다.
//
// 잔여 데이터를 반드시 처리해야 한다면 Close 이전에 생산을 멈추고
// C()를 통해 필요한 만큼 소비한 뒤 Close를 호출하십시오.
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

// Put은 큐에 데이터를 투입합니다. 버퍼가 가득 차 있으면 대기하지 않고 즉시 ErrFull을 반환하며,
// 닫힌 큐에는 ErrClosed를 반환합니다.
//
// nil 반환은 "큐에 투입 성공"을 의미할 뿐, C()로의 전달을 보장하지 않습니다.
// 소비 이전에 Close가 호출되면 해당 데이터는 폐기됩니다. (자세한 내용은 Close 참고)
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
// 등록한 미들웨어는 Put 단계와 Pipe 단계 양쪽에서 모두 실행됩니다.
// 미들웨어는 연산을 중단하거나 취소할 수 없으며, Pipe 단계에서 발생한 panic은 복구되지 않고
// 프로세스를 종료시킵니다. 자세한 내용은 [Context.Next]를 참고하십시오.
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

	return int(q.len.Load())
}

func (q *Queue[T]) Cap() int {
	if q == nil {
		return 0
	}

	// close된 채널에서도 cap 처리 가능함
	return cap(q.inCh)
}

func (q *Queue[T]) IsFull() bool {
	if q == nil {
		return false
	}

	// q.len은 atomic, cap(inCh)은 불변이므로 락이 필요하지 않음
	return int(q.len.Load()) >= cap(q.inCh)
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
	// inCh에 문제가 발생했거나 Close() 메서드 호출된 경우 len을 0으로 초기화
	defer q.len.Store(0)

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
			q.len.Add(-1)

			c.reset()
			q.pool.Put(c)

		case <-q.closeSig: // 고루틴을 종료하면서, outCh를 닫음
			return
		}
	}
}

func (q *Queue[T]) put(data T) (err error) {
	// 배타 락 필수: q.len 검사 -> 카운터 증가 -> inCh 송신이 원자적으로 실행되어야 한다.
	// 읽기 락으로 바꾸면 두 고루틴이 동시에 검사를 통과해 카운터가 깨지고,
	// 버퍼가 넘칠 경우 송신이 영구 대기하며 Close까지 막는다.
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.isClosed {
		return ErrClosed
	}

	if int(q.len.Load()) >= cap(q.inCh) {
		return ErrFull
	}

	q.len.Add(1)
	q.inCh <- data
	return nil
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
