package timer

import (
	"errors"
	"sync/atomic"
	"time"

	"github.com/team-core-1/util/queue"

	"github.com/RussellLuo/timingwheel"
)

var (
	ErrNil          = errors.New("Timer fail(nil)")
	ErrInvalidCapa  = errors.New("Timer fail(invalid capacity)")
	ErrExpiredQFull = errors.New("Timer fail(expired queue full)")
)

type TimerEngine[T any] struct {
	tw  *timingwheel.TimingWheel
	q   *queue.Queue[T]
	len atomic.Uint32
	cap int
}

type Timer struct {
	t        atomic.Pointer[timingwheel.Timer]
	canceled atomic.Bool
}

func New[T any](tw *timingwheel.TimingWheel, capacity int) (*TimerEngine[T], error) {
	if tw == nil {
		return nil, ErrNil
	}

	if capacity <= 0 {
		return nil, ErrInvalidCapa
	}

	q, err := queue.New[T](capacity)
	if err != nil {
		return nil, err
	}

	t := &TimerEngine[T]{
		tw:  tw,
		q:   q,
		cap: capacity,
	}

	return t, nil
}

func (te *TimerEngine[T]) C() <-chan T {
	return te.q.C()
}

func (te *TimerEngine[T]) Set(d time.Duration, key T) (*Timer, error) {
	if te == nil {
		return nil, ErrNil
	}

	max := uint32(te.cap)
	for {
		curr := te.len.Load()

		if curr >= max {
			return nil, ErrExpiredQFull
		}

		if te.len.CompareAndSwap(curr, curr+1) {
			break
		}
	}

	timer := &Timer{}

	f := func() {
		if timer.canceled.CompareAndSwap(false, true) {
			te.len.Add(^uint32(0)) // Add(-1)
			_ = te.q.Enqueue(key)
			timer.t.Store(nil)
		}
	}

	timer.t.Store(te.tw.AfterFunc(d, f))

	return timer, nil
}

func (te *TimerEngine[T]) Cancel(timer *Timer) {
	if (te == nil) || (timer == nil) {
		return
	}

	if !timer.canceled.CompareAndSwap(false, true) {
		return
	}

	te.len.Add(^uint32(0))                // Add(-1)
	if t := timer.t.Swap(nil); t != nil { // t를 nil로 변경하고, t의 이전 값이 있으면 Stop 처리
		t.Stop()
	}
}

func (te *TimerEngine[T]) Len() int {
	return int(te.len.Load())
}

func (te *TimerEngine[T]) Cap() int {
	return te.cap
}
