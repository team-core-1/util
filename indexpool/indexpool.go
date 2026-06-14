package indexpool

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"sync/atomic"
)

var (
	ErrInvalidCap = errors.New("IndexPool fail(invalid capacity)")
	ErrNil        = errors.New("IndexPool fail(nil)")
	ErrEmpty      = errors.New("IndexPool fail(empty)")
)

type IndexPool[T any] struct {
	q     chan int
	seq   []atomic.Uint32
	slots []T
}

func New[T any](capacity int) (*IndexPool[T], error) {
	if capacity <= 0 {
		return nil, ErrInvalidCap
	}

	q := make(chan int, capacity)
	seq := make([]atomic.Uint32, capacity)
	slots := make([]T, capacity)

	for i := range slots {
		seq[i].Store(rand.Uint32())
		q <- i
	}

	return &IndexPool[T]{
		q:     q,
		seq:   seq,
		slots: slots,
	}, nil
}

func (ip *IndexPool[T]) Get() (int, uint32, error) {
	if ip == nil {
		return -1, 0, ErrNil
	}

	select {
	case index := <-ip.q:
		return index, ip.seq[index].Add(1), nil
	default:
		return -1, 0, ErrEmpty
	}
}

func (ip *IndexPool[T]) Put(index int, key uint32) (err error) {
	if ip == nil {
		return fmt.Errorf("IndexPool Put(%d) fail(nil)", index)
	}

	if (index < 0) || (index >= cap(ip.slots)) {
		return fmt.Errorf("IndexPool Put(%d) fail(wrong index)", index)
	}

	if !ip.seq[index].CompareAndSwap(key, key+1) {
		return fmt.Errorf("IndexPool Put(%d) fail(duplicated index)", index)
	}

	ip.slots[index] = *new(T)

	select {
	case ip.q <- index:
		return nil
	default:
		// CAS를 통과한 유효한 slots인데, full이 발생하는 경우는 발생해서는 안됨
		ip.seq[index].Store(key)
		return fmt.Errorf("IndexPool Put(%d) fail(full)", index)
	}
}

func (ip *IndexPool[T]) Len() int {
	return len(ip.q)
}

func (ip *IndexPool[T]) Cap() int {
	return cap(ip.q)
}
