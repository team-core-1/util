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
	ErrInvalidCap            = ErrorType("timer: invalid capacity")
	ErrNil                   = ErrorType("timer: engine is nil")
	ErrNilTimer              = ErrorType("timer: timer is nil")
	ErrNilTimingWheel        = ErrorType("timer: timing wheel is nil")
	ErrClosed                = ErrorType("timer: engine is closed")
	ErrExpiredQueueFull      = ErrorType("timer: expired queue is full")
	ErrAlreadyCancelled      = ErrorType("timer: already cancelled timer")
	ErrNotOwner              = ErrorType("timer: not owner")
	ErrTimeoutDeliveryFailed = ErrorType("timer: timeout delivery failed")
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
		return nil, ErrNilTimingWheel
	}

	if capacity <= 0 {
		return nil, ErrInvalidCap
	}

	pq, err := pipequeue.New[T](capacity)
	if err != nil {
		return nil, err
	}

	eng := &Engine[T]{
		timingWheel: timingWheel,
		pq:          pq,
		cap:         capacity,
	}

	eng.pool.New = func() any {
		return &Context[T]{}
	}

	eng.rebuildHandlers()

	return eng, nil
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
func (eng *Engine[T]) Close() {
	if eng == nil {
		return
	}

	eng.mu.Lock()
	defer eng.mu.Unlock()

	if eng.isClosed {
		return
	}

	eng.isClosed = true
	eng.pq.Close()
}

// Set은 d 시간 뒤에 만료되는 타이머를 등록하고 취소에 사용할 핸들을 반환합니다.
// 만료되면 key가 C()로 전달됩니다.
//
// 보유 중인 타이머 수(대기 중 + 만료 큐 적재분)가 Cap에 도달하면 ErrExpiredQueueFull을,
// 닫힌 엔진이면 ErrClosed를, nil 엔진이면 ErrNil을 반환하며 이때 Timer는 nil입니다.
//
// 반환된 *Timer는 Cancel에만 사용하십시오. 내부 상태를 만료 고루틴과 공유하므로
// 다른 용도로 다루면 안 됩니다.
func (eng *Engine[T]) Set(d time.Duration, key T) (*Timer, error) {
	if eng == nil {
		return nil, ErrNil
	}

	c := eng.pool.Get().(*Context[T])
	defer func() {
		c.reset()
		eng.pool.Put(c)
	}()

	eng.mu.RLock() // RLock()으로 가능한지 확인
	c.handlers = eng.setHandlers
	eng.mu.RUnlock()

	c.index, c.action, c.dur, c.key = -1, ActionSet, d, key
	c.Next()
	return c.timer, c.err
}

// Cancel은 등록된 타이머를 취소합니다.
// 취소에 성공하면 그 키는 C()로 전달되지 않고, 차지하던 정원이 즉시 반납됩니다.
//
//   - 다른 엔진이 발급했거나 직접 만든 Timer면 ErrNotOwner
//   - 이미 취소되었거나 만료 처리가 시작된 타이머면 ErrAlreadyCancelled
//   - nil 엔진이면 ErrNil, nil 타이머면 ErrNilTimer
//
// 소유권을 먼저 검사하는 이유는, 검사가 없으면 취소를 요청한 엔진의 정원 카운터가
// 부당하게 감소해 음수가 되고, 실제 소유 엔진은 정원을 영구히 잠식당하기 때문입니다.
func (eng *Engine[T]) Cancel(timer *Timer) error {
	if eng == nil {
		return ErrNil
	}
	if timer == nil {
		return ErrNilTimer
	}

	c := eng.pool.Get().(*Context[T])
	defer func() {
		c.reset()
		eng.pool.Put(c)
	}()

	eng.mu.RLock()
	c.handlers = eng.cancelHandlers
	eng.mu.RUnlock()

	c.index, c.action, c.timer = -1, ActionCancel, timer
	c.Next()
	return c.err
}

// C는 만료된 키를 수신하는 채널을 반환합니다.
// Set에 넘긴 key가 만료 시점에 이 채널로 전달됩니다.
//
// [동시성 및 주의사항]
//   - 이 채널을 수신하는 소비자가 반드시 존재해야 합니다. 수신자가 없으면 만료된 키가
//     만료 큐에 쌓인 채 정원을 계속 차지하고, 이후 Set은 ErrExpiredQueueFull을 반환합니다.
//     소비를 재개하면 정원도 함께 회복됩니다.
//   - Close 이후 채널이 닫히므로, select case에서 사용하던 엔진은 Close 후 nil로 변경해야 합니다.
func (eng *Engine[T]) C() <-chan T {
	if eng == nil {
		return nil
	}

	return eng.pq.C()
}

// Use는 Set/Cancel/Timeout 연산 전후에 실행할 미들웨어를 체인에 등록합니다.
// 여러 번 호출하면 등록한 순서대로 체인에 누적되며, nil 핸들러는 실행 시 건너뜁니다.
//
// 등록한 미들웨어는 Set/Cancel/Timeout 단계에서 모두 실행됩니다.
// 미들웨어는 연산을 중단하거나 취소할 수 없으며, Timeout 단계에서 발생한 panic은 복구되지 않고
// 프로세스를 종료시킵니다. 자세한 내용은 [Context.Next]를 참고하십시오.
func (eng *Engine[T]) Use(handlerFunc ...HandlerFunc[T]) {
	if eng == nil {
		return
	}

	eng.mu.Lock()
	defer eng.mu.Unlock()

	eng.rebuildHandlers(handlerFunc...)
}

// Len은 현재 사용 중인 타이머 수(대기 중 + 만료 큐 적재분)를 반환합니다.
//
// 두 값을 각각 읽어 더하므로 하나의 시점에 대한 원자적 스냅샷은 아닙니다.
// 집계 중에도 다른 고루틴의 Set/Cancel/만료가 진행될 수 있으므로 근사값으로 사용하십시오.
func (eng *Engine[T]) Len() int {
	if eng == nil {
		return 0
	}

	eng.mu.RLock()
	defer eng.mu.RUnlock()

	return int(eng.active.Load()) + eng.pq.Len()
}

// Cap은 New에 지정한 정원을 반환합니다. Close 이후에도 값이 유지됩니다.
func (eng *Engine[T]) Cap() int {
	if eng == nil {
		return 0
	}

	return eng.cap
}

// IsClosed는 Close가 호출되었는지 알려줍니다. nil 엔진은 true를 반환합니다.
func (eng *Engine[T]) IsClosed() bool {
	if eng == nil {
		return true
	}

	eng.mu.RLock()
	defer eng.mu.RUnlock()

	return eng.isClosed
}

// QFail은 만료되었으나 만료 큐에 넣지 못해 전달이 무산된 키의 누적 개수를 반환합니다.
//
// 정원 검사가 만료 큐 용량과 같은 값을 쓰므로 정상 운영 중에는 큐가 넘치지 않습니다.
// 따라서 이 값은 실질적으로 Close 이후 만료된 타이머 수를 뜻합니다.
// (Close는 대기 중인 타이머를 취소하지 않습니다. 자세한 내용은 Close 참고)
func (eng *Engine[T]) QFail() int {
	if eng == nil {
		return 0
	}

	eng.mu.RLock()
	defer eng.mu.RUnlock()

	return int(eng.qFail.Load())
}

func (eng *Engine[T]) rebuildHandlers(handlerFunc ...HandlerFunc[T]) {
	eng.handlers = append(eng.handlers, handlerFunc...)

	// Middleware + settimer handlers
	eng.setHandlers = make([]HandlerFunc[T], len(eng.handlers)+1)
	copy(eng.setHandlers, eng.handlers)
	eng.setHandlers[len(eng.handlers)] = func(c *Context[T]) {
		c.timer, c.err = eng.setTimer(c.dur, c.key)
	}

	// Middleware + canceltimer handlers
	eng.cancelHandlers = make([]HandlerFunc[T], len(eng.handlers)+1)
	copy(eng.cancelHandlers, eng.handlers)
	eng.cancelHandlers[len(eng.handlers)] = func(c *Context[T]) {
		c.err = eng.cancelTimer(c.timer)
	}

	// Middleware + timeout handlers
	eng.timeoutHandlers = make([]HandlerFunc[T], len(eng.handlers)+1)
	copy(eng.timeoutHandlers, eng.handlers)
	eng.timeoutHandlers[len(eng.handlers)] = func(c *Context[T]) {
		c.err = eng.timeout(c.key)
	}
}

func (eng *Engine[T]) setTimer(d time.Duration, key T) (*Timer, error) {
	eng.mu.RLock()
	if eng.isClosed {
		eng.mu.RUnlock()
		return nil, ErrClosed
	}
	eng.mu.RUnlock()

	// full이면 에러를 리턴하고, 경합이면 CAS로 +1하고 루프 탈출
	for {
		active := eng.active.Load()
		if (int(active) + eng.pq.Len()) >= eng.cap {
			return nil, ErrExpiredQueueFull
		}
		if eng.active.CompareAndSwap(active, active+1) {
			break
		}
	}

	timer := &Timer{
		owner: eng,
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

		defer eng.active.Add(-1)

		c := eng.pool.Get().(*Context[T])
		defer func() {
			c.reset()
			eng.pool.Put(c)
		}()

		eng.mu.RLock()
		c.handlers = eng.timeoutHandlers
		eng.mu.RUnlock()

		c.index, c.action, c.key = -1, ActionTimeout, key
		c.Next()
	}

	timer.timingWheelTimer = eng.timingWheel.AfterFunc(d, timeoutFunc)

	// 등록 직후 Close 여부를 재확인한다.
	//
	// 맨 앞의 검사와 여기 사이에 Close가 완료될 수 있는데, 그대로 두면
	// Set은 성공(nil)과 유효한 *Timer를 반환하지만 그 타이머는 만료 시
	// 닫힌 큐로 유입되어 QFail로만 집계되고 조용히 폐기된다.
	//
	// 읽기 락을 등록 구간 전체에 걸어 두지 않는 이유는, AfterFunc가 timingWheel
	// 내부에서 길게 블록될 수 있어 그동안 쓰기 락을 기다리는 Close/Use가
	// 함께 멈추기 때문이다. (자원이 빠듯한 환경일수록 크게 드러난다.)
	//
	// 되돌리는 동안 timer.mu를 쥐고 있으므로 timeoutFunc와 경합하지 않는다.
	// 되돌린 타이머는 timingWheelTimer가 nil이라 timeoutFunc가 취소로 판정하고
	// active를 감소시키지 않으므로, 여기서의 감소와 합쳐 정확히 1회만 감소한다.
	eng.mu.RLock()
	defer eng.mu.RUnlock()
	if eng.isClosed {
		timer.timingWheelTimer.Stop()
		timer.timingWheelTimer = nil
		eng.active.Add(-1)
		return nil, ErrClosed
	}

	return timer, nil
}

// Cancel은 Set이 완료된 후 timeout과 경합 체크 필요
func (eng *Engine[T]) cancelTimer(timer *Timer) error {
	timer.mu.Lock()
	defer timer.mu.Unlock()

	// 다른 엔진이 발급한 타이머는 취소할 수 없음
	// (소유권 검사가 없으면 요청한 엔진의 active가 부당하게 감소해 음수가 되고,
	//  실제 소유 엔진은 카운터가 줄지 않아 용량을 영구히 잠식당함)
	if timer.owner != any(eng) {
		return ErrNotOwner
	}

	// Timeout 경합, 중복 Cancel 방지
	if timer.timingWheelTimer == nil {
		return ErrAlreadyCancelled
	}

	eng.active.Add(-1)

	timer.timingWheelTimer.Stop()
	timer.timingWheelTimer = nil

	return nil
}

func (eng *Engine[T]) timeout(key T) error {
	if err := eng.pq.Put(key); err != nil {
		eng.qFail.Add(1)
		return ErrTimeoutDeliveryFailed
	}

	return nil
}
