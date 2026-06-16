package mempool

import (
	"errors"
	"math/rand/v2"
	"sync/atomic"
	"unsafe"
)

var (
	ErrInvalidCap = errors.New("MemPool fail(invalid capacity)")
	ErrNil        = errors.New("MemPool fail(nil)")
	ErrChan       = errors.New("MemPool fail(chan)")
	ErrEmpty      = errors.New("MemPool fail(empty)")
	ErrWrongAddr  = errors.New("MemPool fail(wrong address)")
	ErrDupAddr    = errors.New("MemPool fail(duplicated address)")
	ErrClosed     = errors.New("MemPool fail(closed)")
)

type slot[T any] struct {
	index int
	seq   atomic.Uint32
	mem   T
}

type MemPool[T any] struct {
	q        chan int
	slots    [](slot[T])
	baseAddr uintptr
	slotSize uintptr
}

func New[T any](capacity int) (*MemPool[T], error) {
	if capacity <= 0 {
		return nil, ErrInvalidCap
	}

	q := make(chan int, capacity)
	slots := make([](slot[T]), capacity)
	baseAddr := uintptr(unsafe.Pointer(&(slots[0].mem)))
	slotSize := unsafe.Sizeof(slots[0])

	for i := range slots {
		slots[i].index = i
		slots[i].seq.Store(rand.Uint32() &^ 1)
		q <- i
	}

	return &MemPool[T]{
		q:        q,
		slots:    slots,
		baseAddr: baseAddr,
		slotSize: slotSize,
	}, nil
}

// Close 함수는 NewMemPool 직후에 문제가 있으면 사용하고,
// 동작 중에는 가능하면 사용하지 않아야 한다.
func (mp *MemPool[T]) Close() error {
	if mp == nil {
		return ErrNil
	}

	mp.q = nil

	return nil
}

func (mp *MemPool[T]) Get() (mem *T, err error) {
	if mp == nil {
		return nil, ErrNil
	}

	// 레이스 컨디션 방지를 위해 로컬 변수에 복사
	q := mp.q
	if q == nil {
		return nil, ErrClosed
	}

	select {
	case index, ok := <-q:
		if !ok {
			return nil, ErrChan
		}
		slot := &mp.slots[index]
		slot.seq.Add(1) // 짝수(Available) -> 홀수(Busy)
		return &(slot.mem), nil
	default:
		return nil, ErrEmpty
	}
}

func (mp *MemPool[T]) Put(mem *T) (err error) {
	if mp == nil {
		return ErrNil
	}

	// 레이스 컨디션 방지를 위해 로컬 변수에 복사
	q := mp.q
	if q == nil {
		return ErrClosed
	}

	if mem == nil {
		return ErrWrongAddr
	}

	putAddr := uintptr(unsafe.Pointer(mem))
	if putAddr < mp.baseAddr {
		return ErrWrongAddr
	}
	offset := putAddr - mp.baseAddr
	index := int(offset / mp.slotSize)
	if (index >= cap(mp.slots)) || ((offset % mp.slotSize) != 0) {
		return ErrWrongAddr
	}

	slot := &(mp.slots[index])
	seq := slot.seq.Load()

	// 중복 체크
	if (seq % 2) == 0 {
		return ErrDupAddr
	}

	if slot.seq.CompareAndSwap(seq, seq+1) == false {
		return ErrDupAddr
	}

	// 10MB 구조체에 대해서 5배 이상의 비용이 필요
	*mem = *new(T)

	select {
	case q <- index:
		return nil
	default:
		slot.seq.Add(1) // 실패 시 다시 홀수(Busy) 상태로 복구
		return ErrChan
	}
}

func (mp *MemPool[T]) Len() int {
	return len(mp.q)
}

func (mp *MemPool[T]) Cap() int {
	return cap(mp.q)
}
