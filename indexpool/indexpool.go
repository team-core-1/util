package indexpool

import (
	"sync"
)

type ErrorType string

func (e ErrorType) Error() string {
	return string(e)
}

const (
	ErrInvalidCap    = ErrorType("indexPool: invalid capacity")
	ErrNil           = ErrorType("indexPool: pool is nil")
	ErrEmpty         = ErrorType("indexPool: pool is empty")
	ErrWrongIndex    = ErrorType("indexPool: wrong index")
	ErrNotAllocIndex = ErrorType("indexPool: not alloc index")
	ErrInuseIndex    = ErrorType("indexPool: inuse index")
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
)

type HandlerFunc[T any] func(*Context[T])

type slot[T any] struct {
	mu    sync.Mutex
	state State
	mem   T
}

type IndexPool[T any] struct {
	q     chan int
	slots []slot[T]

	mu             sync.RWMutex
	pool           sync.Pool
	handlers       []HandlerFunc[T]
	getHandlers    []HandlerFunc[T]
	putHandlers    []HandlerFunc[T]
	accessHandlers []HandlerFunc[T]
}

func New[T any](capacity int) (*IndexPool[T], error) {
	if capacity <= 0 {
		return nil, ErrInvalidCap
	}

	ip := &IndexPool[T]{
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

func (ip *IndexPool[T]) Get() (int, error) {
	if ip == nil {
		return -1, ErrNil
	}

	c := ip.pool.Get().(*Context[T])
	ip.mu.RLock()
	c.handlers = ip.getHandlers
	ip.mu.RUnlock()
	c.index, c.action = -1, ActionGet

	c.Next()
	idx, err := c.idx, c.err

	c.reset()
	ip.pool.Put(c)

	return idx, err
}

func (ip *IndexPool[T]) Put(index int) error {
	if ip == nil {
		return ErrNil
	}

	if (index < 0) || (index >= len(ip.slots)) {
		return ErrWrongIndex
	}

	c := ip.pool.Get().(*Context[T])
	ip.mu.RLock()
	c.handlers = ip.putHandlers
	ip.mu.RUnlock()
	c.index, c.action, c.idx = -1, ActionPut, index

	c.Next()
	err := c.err

	c.reset()
	ip.pool.Put(c)

	return err
}

func (ip *IndexPool[T]) Access(index int, f func(*T)) error {
	if ip == nil {
		return ErrNil
	}

	if (index < 0) || (index >= len(ip.slots)) {
		return ErrWrongIndex
	}

	c := ip.pool.Get().(*Context[T])
	ip.mu.RLock()
	c.handlers = ip.accessHandlers
	ip.mu.RUnlock()
	c.index, c.action, c.idx, c.fn = -1, ActionAccess, index, f

	c.Next()
	err := c.err

	c.reset()
	ip.pool.Put(c)

	return err
}

func (ip *IndexPool[T]) Use(handlerFunc ...HandlerFunc[T]) {
	ip.rebuildHandlers(handlerFunc...)
}

func (ip *IndexPool[T]) Len() int {
	if ip == nil {
		return 0
	}

	return len(ip.q)
}

func (ip *IndexPool[T]) Cap() int {
	if ip == nil {
		return 0
	}

	return cap(ip.q)
}

func (ip *IndexPool[T]) rebuildHandlers(handlerFunc ...HandlerFunc[T]) {
	ip.mu.Lock()
	defer ip.mu.Unlock()

	ip.handlers = append(ip.handlers, handlerFunc...)

	ip.getHandlers = make([]HandlerFunc[T], len(ip.handlers)+1)
	copy(ip.getHandlers, ip.handlers)
	ip.getHandlers[len(ip.handlers)] = func(c *Context[T]) {
		c.idx, c.err = ip.get()
	}

	ip.putHandlers = make([]HandlerFunc[T], len(ip.handlers)+1)
	copy(ip.putHandlers, ip.handlers)
	ip.putHandlers[len(ip.handlers)] = func(c *Context[T]) {
		c.err = ip.put(c.idx)
	}

	ip.accessHandlers = make([]HandlerFunc[T], len(ip.handlers)+1)
	copy(ip.accessHandlers, ip.handlers)
	ip.accessHandlers[len(ip.handlers)] = func(c *Context[T]) {
		c.err = ip.access(c.idx, c.fn)
		// 동기화가 필요하면 Sync 메서드 사용
		// c.err = ip.accessSync(c.idx, c.fn)
	}
}

func (ip *IndexPool[T]) get() (int, error) {
	select {
	case idx := <-ip.q:
		slot := &ip.slots[idx]
		slot.mu.Lock()
		slot.state = StateAlloc
		slot.mu.Unlock()
		return idx, nil
	default:
		return -1, ErrEmpty
	}
}

func (ip *IndexPool[T]) put(index int) error {
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

func (ip *IndexPool[T]) access(index int, fn func(*T)) error {
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

func (ip *IndexPool[T]) accessSync(index int, fn func(*T)) error {
	slot := &ip.slots[index]

	slot.mu.Lock()
	// f(&slot.mem)에서 panic 발생 시 복구
	defer func() {
		slot.state &^= StateInUse
		slot.mu.Unlock()
	}()

	if (slot.state & StateAlloc) != StateAlloc {
		return ErrNotAllocIndex
	}
	if (slot.state & StateInUse) == StateInUse {
		// 발생할 수 없는 에러
		return ErrInuseIndex
	}
	slot.state |= StateInUse

	fn(&slot.mem)

	return nil
}
