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

type slot[T any] struct {
	mu    sync.Mutex
	state State
	mem   T
}

type IndexPool[T any] struct {
	q     chan int
	slots []slot[T]
}

func New[T any](capacity int) (*IndexPool[T], error) {
	if capacity <= 0 {
		return nil, ErrInvalidCap
	}

	q := make(chan int, capacity)
	slots := make([]slot[T], capacity)

	for i := 0; i < capacity; i++ {
		q <- i
	}

	return &IndexPool[T]{
		q:     q,
		slots: slots,
	}, nil
}

func (ip *IndexPool[T]) Get() (int, error) {
	if ip == nil {
		return -1, ErrNil
	}

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

func (ip *IndexPool[T]) Put(index int) error {
	if ip == nil {
		return ErrNil
	}

	if (index < 0) || (index >= len(ip.slots)) {
		return ErrWrongIndex
	}

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

func (ip *IndexPool[T]) Access(index int, f func(*T)) error {
	if ip == nil {
		return ErrNil
	}

	if (index < 0) || (index >= len(ip.slots)) {
		return ErrWrongIndex
	}

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

	f(&slot.mem)

	return nil
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
