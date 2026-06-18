package timer

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/team-core-1/util/queue"

	"github.com/RussellLuo/timingwheel"
)

var (
	ErrNil          = errors.New("Timer fail(nil)")
	ErrInvalidCap   = errors.New("Timer fail(invalid capacity)")
	ErrExpiredQFull = errors.New("Timer fail(expired queue full)")
	ErrClosed       = errors.New("Timer fail(closed)")
)

type Engine[T any] struct {
	lock        sync.RWMutex
	isClosed    bool
	timingWheel *timingwheel.TimingWheel
	q           *queue.Queue[T]

	qFail  atomic.Int64 // queue 문제로 처리하지 못한 timeout
	active atomic.Int64 // queue에 있는 timeout을 제외한 현재 사용 중인 timer
	cap    int
}

type Timer struct {
	timingWheelTimer *timingwheel.Timer
}

func New[T any](timingWheel *timingwheel.TimingWheel, capacity int) (*Engine[T], error) {
	if timingWheel == nil {
		return nil, ErrNil
	}

	if capacity <= 0 {
		return nil, ErrInvalidCap
	}

	q, err := queue.New[T](capacity)
	if err != nil {
		return nil, err
	}

	engine := &Engine[T]{
		timingWheel: timingWheel,
		q:           q,
		cap:         capacity,
	}

	return engine, nil
}

func (engine *Engine[T]) Close() {
	if engine == nil {
		return
	}

	engine.lock.Lock()
	defer engine.lock.Unlock()

	if engine.isClosed {
		return
	}

	engine.isClosed = true
	engine.q.Close()
}

func (engine *Engine[T]) Set(d time.Duration, key T) (*Timer, error) {
	if engine == nil {
		return nil, ErrNil
	}

	engine.lock.Lock()
	defer engine.lock.Unlock()

	if engine.isClosed {
		return nil, ErrClosed
	}

	if (int(engine.active.Load()) + engine.q.Len()) >= engine.cap {
		return nil, ErrExpiredQFull
	}

	timer := &Timer{}

	timeoutFunc := func() {
		{
			engine.lock.Lock()
			defer engine.lock.Unlock()

			// Cancel 경합
			if timer.timingWheelTimer == nil {
				return
			}

			engine.active.Add(-1)

			timer.timingWheelTimer = nil
		}

		if err := engine.q.Enqueue(key); err != nil {
			engine.qFail.Add(1)
		}

	}

	timer.timingWheelTimer = engine.timingWheel.AfterFunc(d, timeoutFunc)

	engine.active.Add(1)

	return timer, nil
}

// Cancel은 Set이 완료된 후 timeout과 경합 체크 필요
func (engine *Engine[T]) Cancel(timer *Timer) {
	if (engine == nil) || (timer == nil) {
		return
	}

	engine.lock.Lock()
	defer engine.lock.Unlock()

	// Timeout 경합, 중복 Cancel 방지
	if timer.timingWheelTimer == nil {
		return
	}

	engine.active.Add(-1)

	timer.timingWheelTimer.Stop()
	timer.timingWheelTimer = nil
}

func (engine *Engine[T]) C() <-chan T {
	if engine == nil {
		return nil
	}

	return engine.q.C()
}

func (engine *Engine[T]) Len() int {
	if engine == nil {
		return 0
	}

	engine.lock.RLock()
	defer engine.lock.RUnlock()

	return int(engine.active.Load()) + engine.q.Len()
}

func (engine *Engine[T]) Cap() int {
	if engine == nil {
		return 0
	}

	return engine.cap
}

func (engine *Engine[T]) QFail() int {
	if engine == nil {
		return 0
	}

	engine.lock.RLock()
	defer engine.lock.RUnlock()

	return int(engine.qFail.Load())
}

func (engine *Engine[T]) IsClosed() bool {
	if engine == nil {
		return true
	}

	engine.lock.RLock()
	defer engine.lock.RUnlock()

	return engine.isClosed
}
