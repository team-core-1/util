package mempool

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"sync/atomic"
)

var (
	MemPoolErrMpoolNil = errors.New("MemPool Get fail(mpool nil)")
	MemPoolErrGetEmpty = errors.New("MemPool Get fail(mpool empty)")
)

type cell[T any] struct {
	index int
	seq   atomic.Uint32
	mem   T
}

type MemPool[T any] struct {
	queue chan *cell[T]
	cells [](cell[T])
}

func New[T any](capacity uint32) (*MemPool[T], error) {
	queue := make(chan *cell[T], capacity)
	cells := make([](cell[T]), capacity)

	for i := range cells {
		cell := &cells[i]

		cell.index = i
		cell.seq.Store(rand.Uint32())
		queue <- cell
	}

	return &MemPool[T]{
		queue: queue,
		cells: cells,
	}, nil
}

// Close 함수는 NewMemPool 직후에 문제가 있으면 사용하고,
// 동작 중에는 가능하면 사용하지 않아야 한다.
func (mpool *MemPool[T]) Close() error {
	if mpool == nil {
		return MemPoolErrMpoolNil
	}

	mpool.queue = nil

	return nil
}

func (mpool *MemPool[T]) Get() (mem *T, key uint64, err error) {
	if mpool == nil {
		return nil, 0, MemPoolErrMpoolNil
	}

	var cell *cell[T] = nil

	defer func() {
		if r := recover(); r != nil {
			if cell != nil {
				mpool.queue <- cell
			}
			mem = nil
			key = 0
			err = fmt.Errorf("MemPool Get fail(panic: %+v)", r)
		}
	}()

	select {
	case cell = <-mpool.queue:
		return &(cell.mem), packKey(cell.index, cell.seq.Add(1)), nil
	default:
		return nil, 0, MemPoolErrGetEmpty
	}
}

func (mpool *MemPool[T]) Put(key uint64) (err error) {
	index, seq := unpackKey(key)

	if mpool == nil {
		return fmt.Errorf("MemPool Put(%d) fail(mpool nil)", index)
	}

	if (index < 0) || (index >= cap(mpool.cells)) {
		return fmt.Errorf("MemPool Put(%d) fail(wrong index)", index)
	}

	cell := &(mpool.cells[index])

	defer func() {
		if r := recover(); r != nil {
			cell.seq.Store(seq)
			err = fmt.Errorf("MemPool Put(%d) fail(panic: %+v)", index, r)
		}
	}()

	if cell.seq.CompareAndSwap(seq, seq+1) == false {
		return fmt.Errorf("MemPool Put(%d) fail(duplicated index)", index)
	}

	select {
	case mpool.queue <- cell:
		return nil
	default:
		// CAS를 통과한 유효한 cell인데, full이 발생하는 경우는 발생해서는 안됨
		cell.seq.Store(seq)
		return fmt.Errorf("MemPool Put(%d) fail(mpool full)", index)
	}
}

func (mpool *MemPool[T]) String() string {
	if mpool == nil {
		return fmt.Sprintf("MemPool String fail(mpool nil)")
	}

	return fmt.Sprintf("{\"MemPool\": {\"len\": %d, \"cap\": %d}}", len(mpool.queue), cap(mpool.queue))
}

func packKey(index int, seq uint32) uint64 {
	return (uint64(index) << 32) | uint64(seq)
}

func unpackKey(key uint64) (int, uint32) {
	return int(key >> 32), uint32(key)
}
