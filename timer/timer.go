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
	ErrNotOwner         = ErrorType("timer: not owner")
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
	owner            any
}

// New는 타이머 엔진을 생성합니다.
//
// timingWheel은 외부에서 주입받으며, 호출 측이 Start/Stop 수명 주기를 책임집니다.
// New는 timingWheel이 이미 Start된 상태인지 검사하지 않으므로,
// Start하지 않은 채 전달하면 타이머가 만료되지 않습니다.
//
// capacity는 동시에 보유할 수 있는 타이머 수(대기 중 + 만료 큐 적재분)의 상한입니다.
// 상한에 도달한 상태에서 Set을 호출하면 ErrExpiredQueueFull을 반환합니다.
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

// Close는 엔진을 닫고 내부 만료 큐를 해제합니다. 여러 번 호출해도 안전합니다.
//
// [대기 중인 타이머는 취소되지 않습니다]
// Close는 이미 Set된 타이머를 일괄 취소하지 않습니다.
// 엔진이 발급한 *Timer 핸들을 내부에 보관하지 않으므로 일괄 취소를 지원할 수 없습니다.
//
// 따라서 Close 이후에도 대기 중이던 타이머는 예정대로 만료되며,
// 만료된 키는 닫힌 큐로 유입되어 전달되지 못하고 QFail 카운터로만 집계됩니다.
// 개별 취소가 필요하면 Set이 반환한 *Timer를 호출 측에서 보관했다가 Close 이전에 Cancel하십시오.
//
// timingWheel은 외부에서 주입받은 자원이므로 Close가 정지시키지 않습니다.
// 소유자가 직접 Stop을 호출해야 합니다.
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

// Use는 Set/Cancel/Timeout 연산 전후에 실행할 미들웨어를 체인에 등록합니다.
// 여러 번 호출하면 등록한 순서대로 체인에 누적되며, nil 핸들러는 실행 시 건너뜁니다.
//
// 등록한 미들웨어는 Set/Cancel/Timeout 단계에서 모두 실행됩니다.
// 미들웨어는 연산을 중단하거나 취소할 수 없으며, Timeout 단계에서 발생한 panic은 복구되지 않고
// 프로세스를 종료시킵니다. 자세한 내용은 [Context.Next]를 참고하십시오.
func (engine *Engine[T]) Use(handlerFunc ...HandlerFunc[T]) {
	if engine == nil {
		return
	}

	engine.mu.Lock()
	defer engine.mu.Unlock()

	engine.rebuildHandlers(handlerFunc...)
}

// Len은 현재 사용 중인 타이머 수(대기 중 + 만료 큐 적재분)를 반환합니다.
//
// 두 값을 각각 읽어 더하므로 하나의 시점에 대한 원자적 스냅샷은 아닙니다.
// 집계 중에도 다른 고루틴의 Set/Cancel/만료가 진행될 수 있으므로 근사값으로 사용하십시오.
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

	timer := &Timer{
		owner: engine,
	}

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

	// 다른 엔진이 발급한 타이머는 취소할 수 없음
	// (소유권 검사가 없으면 요청한 엔진의 active가 부당하게 감소해 음수가 되고,
	//  실제 소유 엔진은 카운터가 줄지 않아 용량을 영구히 잠식당함)
	if timer.owner != any(engine) {
		return ErrNotOwner
	}

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
	if err := engine.pq.Put(key); err != nil {
		engine.qFail.Add(1)
		return ErrExpiredQueueFail
	}

	return nil
}
