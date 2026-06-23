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
	ErrInuseIndex    = ErrorType("IndexPool fail(inuse index)")
	ErrNotInuseIndex = ErrorType("IndexPool fail(not inuse index)")
	ErrDupIndex      = ErrorType("IndexPool fail(duplicated index)")
)

type ActionType int

const (
	ActionGet ActionType = iota + 1
	ActionPut
)

type slot[T any] struct {
	inUse bool
	mem   T
}

type IndexPool[T any] struct {
	mu    sync.RWMutex
	q     chan int
	slots [](slot[T])
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

func (ip *IndexPool[T]) GetIndex() (int, error) {
	if ip == nil {
		return -1, ErrNil
	}

	select {
	case idx := <-ip.q:
		ip.mu.Lock()
		ip.slots[idx].inUse = true
		ip.mu.Unlock()
		return idx, nil
	default:
		return -1, ErrEmpty
	}
}

func (ip *IndexPool[T]) GetMem(index int) (*T, error) {
	if ip == nil {
		return nil, ErrNil
	}

	if (index < 0) || (index >= len(ip.slots)) {
		return nil, ErrWrongIndex
	}

	ip.mu.RLock()
	defer ip.mu.RUnlock()

	if !ip.slots[index].inUse {
		return nil, ErrNotInuseIndex
	}

	return &ip.slots[index].mem, nil
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

	if !ip.slots[index].inUse {
		return ErrDupIndex
	}
	ip.slots[index].inUse = false

	var zero T
	ip.slots[index].mem = zero

	ip.q <- index

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
