package indexmempool

import (
	"sync"
)

type ErrorType string

func (e ErrorType) Error() string {
	return string(e)
}

const (
	ErrInvalidCap    = ErrorType("indexmempool: invalid capacity")
	ErrNil           = ErrorType("indexmempool: pool is nil")
	ErrEmpty         = ErrorType("indexmempool: pool is empty")
	ErrInvalidIndex  = ErrorType("indexmempool: invalid index")
	ErrNotAllocIndex = ErrorType("indexmempool: not alloc index")
	ErrInuseIndex    = ErrorType("indexmempool: inuse index")
)

type State int

const (
	StateNone  State = 0
	StateAlloc State = 1 << (iota - 1)
	StateInUse
)

type ActionType int

const (
	ActionGet ActionType = iota + 1
	ActionPut
	ActionAccess
	ActionAccessLock
)

type HandlerFunc[T any] func(*Context[T])

type slot[T any] struct {
	mu    sync.Mutex
	state State
	mem   T
}

type Pool[T any] struct {
	mu    sync.RWMutex
	q     chan int
	slots []slot[T]

	pool               sync.Pool
	handlers           []HandlerFunc[T]
	getHandlers        []HandlerFunc[T]
	putHandlers        []HandlerFunc[T]
	accessHandlers     []HandlerFunc[T]
	accessLockHandlers []HandlerFunc[T]
}

func New[T any](capacity int) (*Pool[T], error) {
	if capacity <= 0 {
		return nil, ErrInvalidCap
	}

	ip := &Pool[T]{
		q:     make(chan int, capacity),
		slots: make([]slot[T], capacity),
	}

	for i := 0; i < capacity; i++ {
		ip.q <- i
	}

	ip.pool.New = func() any {
		return &Context[T]{}
	}

	ip.rebuildHandlers()

	return ip, nil
}

func (ip *Pool[T]) Get() (int, error) {
	if ip == nil {
		return -1, ErrNil
	}

	c := ip.pool.Get().(*Context[T])
	defer func() {
		c.reset()
		ip.pool.Put(c)
	}()

	ip.mu.RLock()
	c.handlers = ip.getHandlers
	ip.mu.RUnlock()

	c.index, c.action = -1, ActionGet
	c.Next()
	return c.slotIndex, c.err
}

func (ip *Pool[T]) Put(index int) error {
	if ip == nil {
		return ErrNil
	}

	if (index < 0) || (index >= len(ip.slots)) {
		return ErrInvalidIndex
	}

	c := ip.pool.Get().(*Context[T])
	defer func() {
		c.reset()
		ip.pool.Put(c)
	}()

	ip.mu.RLock()
	c.handlers = ip.putHandlers
	ip.mu.RUnlock()

	c.index, c.action, c.slotIndex = -1, ActionPut, index
	c.Next()
	return c.err
}

func (ip *Pool[T]) Access(index int, f func(*T)) error {
	if ip == nil {
		return ErrNil
	}

	if (index < 0) || (index >= len(ip.slots)) {
		return ErrInvalidIndex
	}

	c := ip.pool.Get().(*Context[T])
	defer func() {
		c.reset()
		ip.pool.Put(c)
	}()

	ip.mu.RLock()
	c.handlers = ip.accessHandlers
	ip.mu.RUnlock()

	c.index, c.action, c.slotIndex, c.fn = -1, ActionAccess, index, f
	c.Next()
	return c.err
}

func (ip *Pool[T]) AccessLock(index int, f func(*T)) error {
	if ip == nil {
		return ErrNil
	}

	if (index < 0) || (index >= len(ip.slots)) {
		return ErrInvalidIndex
	}

	c := ip.pool.Get().(*Context[T])
	defer func() {
		c.reset()
		ip.pool.Put(c)
	}()

	ip.mu.RLock()
	c.handlers = ip.accessLockHandlers
	ip.mu.RUnlock()

	c.index, c.action, c.slotIndex, c.fn = -1, ActionAccessLock, index, f
	c.Next()
	return c.err
}

// Use는 Get/Put/Access/AccessLock 연산 전후에 실행할 미들웨어를 체인에 등록합니다.
// 여러 번 호출하면 등록한 순서대로 체인에 누적되며, nil 핸들러는 실행 시 건너뜁니다.
//
// 미들웨어는 연산을 중단하거나 취소할 수 없습니다. 자세한 내용은 [Context.Next]를 참고하십시오.
func (ip *Pool[T]) Use(handlerFunc ...HandlerFunc[T]) {
	if ip == nil {
		return
	}

	ip.mu.Lock()
	defer ip.mu.Unlock()

	ip.rebuildHandlers(handlerFunc...)
}

func (ip *Pool[T]) Len() int {
	if ip == nil {
		return 0
	}

	return len(ip.q)
}

func (ip *Pool[T]) Cap() int {
	if ip == nil {
		return 0
	}

	return cap(ip.q)
}

func (ip *Pool[T]) rebuildHandlers(handlerFunc ...HandlerFunc[T]) {
	ip.handlers = append(ip.handlers, handlerFunc...)

	ip.getHandlers = make([]HandlerFunc[T], len(ip.handlers)+1)
	copy(ip.getHandlers, ip.handlers)
	ip.getHandlers[len(ip.handlers)] = func(c *Context[T]) {
		c.slotIndex, c.err = ip.get()
	}

	ip.putHandlers = make([]HandlerFunc[T], len(ip.handlers)+1)
	copy(ip.putHandlers, ip.handlers)
	ip.putHandlers[len(ip.handlers)] = func(c *Context[T]) {
		c.err = ip.put(c.slotIndex)
	}

	ip.accessHandlers = make([]HandlerFunc[T], len(ip.handlers)+1)
	copy(ip.accessHandlers, ip.handlers)
	ip.accessHandlers[len(ip.handlers)] = func(c *Context[T]) {
		c.err = ip.access(c.slotIndex, c.fn)
	}

	ip.accessLockHandlers = make([]HandlerFunc[T], len(ip.handlers)+1)
	copy(ip.accessLockHandlers, ip.handlers)
	ip.accessLockHandlers[len(ip.handlers)] = func(c *Context[T]) {
		c.err = ip.accessLock(c.slotIndex, c.fn)
	}
}

func (ip *Pool[T]) get() (int, error) {
	select {
	case slotIndex := <-ip.q:
		slot := &ip.slots[slotIndex]
		slot.mu.Lock()
		slot.state = StateAlloc
		slot.mu.Unlock()
		return slotIndex, nil
	default:
		return -1, ErrEmpty
	}
}

func (ip *Pool[T]) put(index int) error {
	slot := &ip.slots[index]

	slot.mu.Lock()
	if (slot.state & StateAlloc) != StateAlloc {
		slot.mu.Unlock()
		return ErrNotAllocIndex
	}
	if (slot.state & StateInUse) == StateInUse {
		slot.mu.Unlock()
		return ErrInuseIndex
	}
	slot.state = StateNone

	var zero T
	slot.mem = zero
	slot.mu.Unlock()

	ip.q <- index

	return nil
}

func (ip *Pool[T]) access(index int, fn func(*T)) error {
	slot := &ip.slots[index]

	slot.mu.Lock()
	if (slot.state & StateAlloc) != StateAlloc {
		slot.mu.Unlock()
		return ErrNotAllocIndex
	}
	if (slot.state & StateInUse) == StateInUse {
		slot.mu.Unlock()
		return ErrInuseIndex
	}
	slot.state |= StateInUse
	slot.mu.Unlock()

	// f(&slot.mem)에서 panic 발생 시 복구
	defer func() {
		slot.mu.Lock()
		slot.state &^= StateInUse
		slot.mu.Unlock()
	}()

	fn(&slot.mem)

	return nil
}

func (ip *Pool[T]) accessLock(index int, fn func(*T)) error {
	slot := &ip.slots[index]

	slot.mu.Lock()
	defer slot.mu.Unlock()

	if (slot.state & StateAlloc) != StateAlloc {
		return ErrNotAllocIndex
	}
	if (slot.state & StateInUse) == StateInUse {
		// Access()가 락을 놓고 콜백 실행 중인 경우 도달.
		// 이 시점에는 StateInUse의 소유자가 Access()이므로 절대 해제하면 안 됨.
		return ErrInuseIndex
	}
	slot.state |= StateInUse

	// f(&slot.mem)에서 panic 발생 시, 이 함수가 세운 StateInUse만 복구
	defer func() {
		slot.state &^= StateInUse
	}()

	fn(&slot.mem)

	return nil
}
