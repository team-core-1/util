package indexpool

import (
	"sync"
)

type ErrorType string

func (e ErrorType) Error() string {
	return string(e)
}

const (
	ErrInvalidCap    = ErrorType("IndexPool fail(invalid capacity)")
	ErrNil           = ErrorType("IndexPool fail(nil)")
	ErrEmpty         = ErrorType("IndexPool fail(empty)")
	ErrWrongIndex    = ErrorType("IndexPool fail(wrong index)")
	ErrNotAllocIndex = ErrorType("IndexPool fail(not alloc index)")
	ErrInuseIndex    = ErrorType("IndexPool fail(inuse index)")
)

type State int

const (
	StateNone  State = 0
	StateAlloc State = 1 << iota
	StateInUse
)

type slot[T any] struct {
	state State
	mem   T
}

type IndexPool[T any] struct {
	mu    sync.Mutex
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
		ip.mu.Lock()
		ip.slots[idx].state = StateAlloc
		ip.mu.Unlock()
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

	ip.mu.Lock()
	defer ip.mu.Unlock()

	if (ip.slots[index].state & StateAlloc) != StateAlloc {
		return ErrNotAllocIndex
	}
	if (ip.slots[index].state & StateInUse) == StateInUse {
		return ErrInuseIndex
	}
	ip.slots[index].state = StateNone

	var zero T
	ip.slots[index].mem = zero

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

	if err := func() error {
		ip.mu.Lock()
		defer ip.mu.Unlock()

		if (ip.slots[index].state & StateAlloc) != StateAlloc {
			return ErrNotAllocIndex
		}
		if (ip.slots[index].state & StateInUse) == StateInUse {
			return ErrInuseIndex
		}
		ip.slots[index].state |= StateInUse

		return nil
	}(); err != nil {
		return err
	}

	defer func() {
		ip.mu.Lock()
		ip.slots[index].state &^= StateInUse
		ip.mu.Unlock()
	}()

	f(&ip.slots[index].mem)

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
