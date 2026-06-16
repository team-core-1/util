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
	twt      atomic.Pointer[timingwheel.Timer]
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

	te := &TimerEngine[T]{
		tw:  tw,
		q:   q,
		cap: capacity,
	}

	return te, nil
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
			timer.twt.Store(nil)
		}
	}

	twt := te.tw.AfterFunc(d, f)
	timer.twt.Store(twt)

	// 타이머를 생성하는 찰나에 이미 만료(timeout)된 경우를 대비
	// f()가 Store(nil)을 먼저 수행하고, 여기서 다시 Store(twt)를 했을 경우를 방어함
	if timer.canceled.Load() {
		// 이미 만료되었으므로 Stop() 호출 없이 포인터만 정리
		timer.twt.Store(nil)
	}

	return timer, nil
}

// Cancel은 Set이 완료된 후 timeout과 경합 체크 필요
func (te *TimerEngine[T]) Cancel(timer *Timer) {
	if (te == nil) || (timer == nil) {
		return
	}

	// 이미 만료되었거나 취소된 경우 중복 처리 방지
	if !timer.canceled.CompareAndSwap(false, true) {
		return
	}

	te.len.Add(^uint32(0))                      // uint32에서의 Add(-1) 효과
	if twt := timer.twt.Swap(nil); twt != nil { // t를 nil로 변경하고, t의 이전 값이 있으면 Stop 처리
		twt.Stop()
	}
}

func (te *TimerEngine[T]) Len() int {
	return int(te.len.Load())
}

func (te *TimerEngine[T]) Cap() int {
	return te.cap
}
