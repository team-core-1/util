package timer

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/RussellLuo/timingwheel"
	"github.com/team-core-1/util/pipequeue"
)

type ErrorType string

func (e ErrorType) Error() string {
	return string(e)
}

const (
	ErrInvalidCap       = ErrorType("timer: invalid capacity")
	ErrNil              = ErrorType("timer: nil")
	ErrClosed           = ErrorType("timer: closed")
	ErrExpiredQueueFull = ErrorType("timer: expired queue full")
	ErrAlreadyCancelled = ErrorType("timer: already cancelled timer")
	ErrExpiredQueueFail = ErrorType("timer: expired queue fail")
)

type ActionType int

const (
	ActionSet ActionType = iota + 1
	ActionCancel
	ActionTimeout
)

type HandlerFunc[T any] func(*Context[T])

type Engine[T any] struct {
	mu          sync.RWMutex
	isClosed    bool
	timingWheel *timingwheel.TimingWheel
	pq          *pipequeue.Queue[T]

	qFail  atomic.Int64 // queue 문제로 처리하지 못한 timeout
	active atomic.Int64 // queue에 있는 timeout을 제외한 현재 사용 중인 timer
	cap    int

	pool            sync.Pool
	handlers        []HandlerFunc[T]
	setHandlers     []HandlerFunc[T]
	cancelHandlers  []HandlerFunc[T]
	timeoutHandlers []HandlerFunc[T]
}

type Timer struct {
	mu               sync.Mutex
	timingWheelTimer *timingwheel.Timer
}

func New[T any](timingWheel *timingwheel.TimingWheel, capacity int) (*Engine[T], error) {
	if timingWheel == nil {
		return nil, ErrNil
	}

	if capacity <= 0 {
		return nil, ErrInvalidCap
	}

	pq, err := pipequeue.New[T](capacity)
	if err != nil {
		return nil, err
	}

	engine := &Engine[T]{
		timingWheel: timingWheel,
		pq:          pq,
		cap:         capacity,
	}

	engine.pool.New = func() any {
		return &Context[T]{}
	}

	engine.rebuildHandlers()

	return engine, nil
}

func (engine *Engine[T]) Close() {
	if engine == nil {
		return
	}

	engine.mu.Lock()
	defer engine.mu.Unlock()

	if engine.isClosed {
		return
	}

	engine.isClosed = true
	engine.pq.Close()
}

func (engine *Engine[T]) Set(d time.Duration, key T) (*Timer, error) {
	if engine == nil {
		return nil, ErrNil
	}

	c := engine.pool.Get().(*Context[T])
	defer func() {
		c.reset()
		engine.pool.Put(c)
	}()

	engine.mu.RLock() // RLock()으로 가능한지 확인
	c.handlers = engine.setHandlers
	engine.mu.RUnlock()

	c.index, c.action, c.dur, c.key = -1, ActionSet, d, key
	c.Next()
	return c.timer, c.err
}

func (engine *Engine[T]) Cancel(timer *Timer) error {
	if (engine == nil) || (timer == nil) {
		return ErrNil
	}

	c := engine.pool.Get().(*Context[T])
	defer func() {
		c.reset()
		engine.pool.Put(c)
	}()

	engine.mu.RLock()
	c.handlers = engine.cancelHandlers
	engine.mu.RUnlock()

	c.index, c.action, c.timer = -1, ActionCancel, timer
	c.Next()
	return c.err
}

func (engine *Engine[T]) C() <-chan T {
	if engine == nil {
		return nil
	}

	return engine.pq.C()
}

func (engine *Engine[T]) Use(handlerFunc ...HandlerFunc[T]) {
	if engine == nil {
		return
	}

	engine.rebuildHandlers(handlerFunc...)
}

func (engine *Engine[T]) Len() int {
	if engine == nil {
		return 0
	}

	engine.mu.RLock()
	defer engine.mu.RUnlock()

	return int(engine.active.Load()) + engine.pq.Len()
}

func (engine *Engine[T]) Cap() int {
	if engine == nil {
		return 0
	}

	return engine.cap
}

func (engine *Engine[T]) IsClosed() bool {
	if engine == nil {
		return true
	}

	engine.mu.RLock()
	defer engine.mu.RUnlock()

	return engine.isClosed
}

func (engine *Engine[T]) QFail() int {
	if engine == nil {
		return 0
	}

	engine.mu.RLock()
	defer engine.mu.RUnlock()

	return int(engine.qFail.Load())
}

func (engine *Engine[T]) rebuildHandlers(handlerFunc ...HandlerFunc[T]) {
	engine.mu.Lock()
	defer engine.mu.Unlock()

	engine.handlers = append(engine.handlers, handlerFunc...)

	// Middleware + settimer handlers
	engine.setHandlers = make([]HandlerFunc[T], len(engine.handlers)+1)
	copy(engine.setHandlers, engine.handlers)
	engine.setHandlers[len(engine.handlers)] = func(c *Context[T]) {
		c.timer, c.err = engine.setTimer(c.dur, c.key)
	}

	// Middleware + canceltimer handlers
	engine.cancelHandlers = make([]HandlerFunc[T], len(engine.handlers)+1)
	copy(engine.cancelHandlers, engine.handlers)
	engine.cancelHandlers[len(engine.handlers)] = func(c *Context[T]) {
		c.err = engine.cancelTimer(c.timer)
	}

	// Middleware + timeout handlers
	engine.timeoutHandlers = make([]HandlerFunc[T], len(engine.handlers)+1)
	copy(engine.timeoutHandlers, engine.handlers)
	engine.timeoutHandlers[len(engine.handlers)] = func(c *Context[T]) {
		c.err = engine.timeout(c.key)
	}
}

func (engine *Engine[T]) setTimer(d time.Duration, key T) (*Timer, error) {
	engine.mu.RLock()
	if engine.isClosed {
		engine.mu.RUnlock()
		return nil, ErrClosed
	}
	engine.mu.RUnlock()

	// full이면 에러를 리턴하고, 경합이면 CAS로 +1하고 루프 탈출
	for {
		active := engine.active.Load()
		if (int(active) + engine.pq.Len()) >= engine.cap {
			return nil, ErrExpiredQueueFull
		}
		if engine.active.CompareAndSwap(active, active+1) {
			break
		}
	}

	timer := &Timer{}

	timer.mu.Lock()
	defer timer.mu.Unlock()

	timeoutFunc := func() {
		if isCancelled := func() bool {
			timer.mu.Lock()
			defer timer.mu.Unlock()

			// Cancel 경합
			if timer.timingWheelTimer == nil {
				return true
			}
			timer.timingWheelTimer = nil
			return false
		}(); isCancelled {
			return
		}

		defer engine.active.Add(-1)

		c := engine.pool.Get().(*Context[T])
		defer func() {
			c.reset()
			engine.pool.Put(c)
		}()

		engine.mu.RLock()
		c.handlers = engine.timeoutHandlers
		engine.mu.RUnlock()

		c.index, c.action, c.key = -1, ActionTimeout, key
		c.Next()
	}

	timer.timingWheelTimer = engine.timingWheel.AfterFunc(d, timeoutFunc)

	return timer, nil
}

// Cancel은 Set이 완료된 후 timeout과 경합 체크 필요
func (engine *Engine[T]) cancelTimer(timer *Timer) error {
	timer.mu.Lock()
	defer timer.mu.Unlock()

	// Timeout 경합, 중복 Cancel 방지
	if timer.timingWheelTimer == nil {
		return ErrAlreadyCancelled
	}

	engine.active.Add(-1)

	timer.timingWheelTimer.Stop()
	timer.timingWheelTimer = nil

	return nil
}

func (engine *Engine[T]) timeout(key T) error {
	if err := engine.pq.Enqueue(key); err != nil {
		engine.qFail.Add(1)
		return ErrExpiredQueueFail
	}

	return nil
}
