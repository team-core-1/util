package indexmempool

import (
	"sync"
)

type ErrorType string

func (e ErrorType) Error() string {
	return string(e)
}

const (
	ErrInvalidCap    = ErrorType("indexmempool: invalid capacity")
	ErrNil           = ErrorType("indexmempool: pool is nil")
	ErrEmpty         = ErrorType("indexmempool: pool is empty")
	ErrInvalidIndex  = ErrorType("indexmempool: invalid index")
	ErrNotAllocIndex = ErrorType("indexmempool: not alloc index")
	ErrInuseIndex    = ErrorType("indexmempool: inuse index")
)

type State int

const (
	StateNone  State = 0
	StateAlloc State = 1 << (iota - 1)
	StateInUse
)

type ActionType int

const (
	ActionGet ActionType = iota + 1
	ActionPut
	ActionAccess
	ActionAccessLock
)

type HandlerFunc[T any] func(*Context[T])

type slot[T any] struct {
	mu    sync.Mutex
	state State
	mem   T
}

type Pool[T any] struct {
	mu    sync.RWMutex
	q     chan int
	slots []slot[T]

	pool               sync.Pool
	handlers           []HandlerFunc[T]
	getHandlers        []HandlerFunc[T]
	putHandlers        []HandlerFunc[T]
	accessHandlers     []HandlerFunc[T]
	accessLockHandlers []HandlerFunc[T]
}

func New[T any](capacity int) (*Pool[T], error) {
	if capacity <= 0 {
		return nil, ErrInvalidCap
	}

	ip := &Pool[T]{
		q:     make(chan int, capacity),
		slots: make([]slot[T], capacity),
	}

	for i := 0; i < capacity; i++ {
		ip.q <- i
	}

	ip.pool.New = func() any {
		return &Context[T]{}
	}

	ip.rebuildHandlers()

	return ip, nil
}

// Get은 사용 가능한 슬롯 하나를 할당하고 그 인덱스를 반환합니다. (비블로킹)
// 여유 슬롯이 없으면 대기하지 않고 즉시 ErrEmpty를 반환합니다.
// 할당된 인덱스는 Access/AccessLock으로 접근하고, 사용이 끝나면 Put으로 반납하십시오.
func (ip *Pool[T]) Get() (int, error) {
	if ip == nil {
		return -1, ErrNil
	}

	c := ip.pool.Get().(*Context[T])
	defer func() {
		c.reset()
		ip.pool.Put(c)
	}()

	ip.mu.RLock()
	c.handlers = ip.getHandlers
	ip.mu.RUnlock()

	c.index, c.action = -1, ActionGet
	c.Next()
	return c.slotIndex, c.err
}

// Put은 할당된 슬롯을 반납합니다. 슬롯의 메모리는 제로값으로 초기화된 뒤 재사용 대기열로 돌아갑니다.
//   - 할당되지 않은 인덱스(중복 반납 포함)면 ErrNotAllocIndex를 반환합니다.
//   - Access/AccessLock 콜백이 실행 중인 슬롯이면 ErrInuseIndex를 반환하며 반납되지 않습니다.
//   - 범위를 벗어난 인덱스면 ErrInvalidIndex를 반환합니다.
//
// 반납 이후 해당 인덱스는 다른 사용자에게 재할당될 수 있으므로,
// 이전에 얻은 메모리 포인터를 계속 사용해서는 안 됩니다.
func (ip *Pool[T]) Put(index int) error {
	if ip == nil {
		return ErrNil
	}

	if (index < 0) || (index >= len(ip.slots)) {
		return ErrInvalidIndex
	}

	c := ip.pool.Get().(*Context[T])
	defer func() {
		c.reset()
		ip.pool.Put(c)
	}()

	ip.mu.RLock()
	c.handlers = ip.putHandlers
	ip.mu.RUnlock()

	c.index, c.action, c.slotIndex = -1, ActionPut, index
	c.Next()
	return c.err
}

// Access는 할당된 슬롯의 메모리 포인터를 콜백 f에 전달합니다. (비블로킹 방식)
//
// 콜백 실행 전에 슬롯 락을 해제하고 사용 중 표시(StateInUse)만 남기므로,
// 콜백이 오래 걸려도 다른 슬롯의 연산을 막지 않습니다.
// 대신 같은 슬롯에 대한 중복 접근은 대기하지 않고 즉시 ErrInuseIndex로 거부됩니다.
//
// [AccessLock과의 차이 - 같은 인덱스에 동시 접근 시]
//
//	Access 실행 중  -> Access      : ErrInuseIndex 즉시 반환 (대기하지 않음)
//	Access 실행 중  -> AccessLock  : ErrInuseIndex 즉시 반환 (대기하지 않음)
//	AccessLock 실행 중 -> Access   : 슬롯 락에서 대기 후 실행
//
// 즉 Access는 "실패를 즉시 알고 재시도"하는 용도이고,
// AccessLock은 "순서를 기다려서라도 반드시 실행"하는 용도입니다.
//
// [주의사항]
//   - 콜백 f는 슬롯 락을 잡지 않은 상태로 실행됩니다.
//     f에 전달된 포인터를 콜백 밖으로 반출하지 마십시오. Put 이후 다른 사용자에게 재할당됩니다.
//   - 콜백에서 panic이 발생해도 StateInUse는 복구되지만, panic 자체는 호출 측으로 전파됩니다.
//   - 할당되지 않은 슬롯이면 ErrNotAllocIndex, 범위를 벗어난 인덱스면 ErrInvalidIndex를 반환합니다.
func (ip *Pool[T]) Access(index int, f func(*T)) error {
	if ip == nil {
		return ErrNil
	}

	if (index < 0) || (index >= len(ip.slots)) {
		return ErrInvalidIndex
	}

	c := ip.pool.Get().(*Context[T])
	defer func() {
		c.reset()
		ip.pool.Put(c)
	}()

	ip.mu.RLock()
	c.handlers = ip.accessHandlers
	ip.mu.RUnlock()

	c.index, c.action, c.slotIndex, c.fn = -1, ActionAccess, index, f
	c.Next()
	return c.err
}

// AccessLock은 할당된 슬롯의 메모리 포인터를 콜백 f에 전달합니다. (블로킹 방식)
//
// Access와 달리 콜백 실행 구간 전체에서 슬롯 락을 유지하므로,
// 같은 슬롯에 대한 다른 AccessLock/Access 호출은 거부되지 않고 순서를 기다려 직렬 실행됩니다.
// 콜백 안에서 읽고-수정하는 연산의 원자성이 필요할 때 사용하십시오.
//
// [Access와의 차이 - 같은 인덱스에 동시 접근 시]
//
//	AccessLock 실행 중 -> AccessLock : 슬롯 락에서 대기 후 실행
//	AccessLock 실행 중 -> Access     : 슬롯 락에서 대기 후 실행
//	Access 실행 중     -> AccessLock : ErrInuseIndex 즉시 반환
//	  (Access는 락을 놓고 콜백을 실행하므로 락 획득에는 성공하지만,
//	   사용 중 표시가 남아 있어 소유자를 침범하지 않고 거부합니다.)
//
// [주의사항]
//   - 콜백이 길어지면 같은 슬롯에 접근하려는 다른 고루틴이 그만큼 대기합니다.
//     I/O 대기 등 오래 걸리는 작업은 콜백 밖에서 처리하십시오.
//   - 콜백 f 안에서 같은 인덱스에 대해 Access/AccessLock/Put을 호출하면
//     이미 자신이 슬롯 락을 쥐고 있으므로 자기 데드락에 빠집니다.
//   - f에 전달된 포인터를 콜백 밖으로 반출하지 마십시오. Put 이후 다른 사용자에게 재할당됩니다.
//   - 콜백에서 panic이 발생해도 슬롯 락과 StateInUse는 복구되지만, panic 자체는 호출 측으로 전파됩니다.
//   - 할당되지 않은 슬롯이면 ErrNotAllocIndex, 범위를 벗어난 인덱스면 ErrInvalidIndex를 반환합니다.
func (ip *Pool[T]) AccessLock(index int, f func(*T)) error {
	if ip == nil {
		return ErrNil
	}

	if (index < 0) || (index >= len(ip.slots)) {
		return ErrInvalidIndex
	}

	c := ip.pool.Get().(*Context[T])
	defer func() {
		c.reset()
		ip.pool.Put(c)
	}()

	ip.mu.RLock()
	c.handlers = ip.accessLockHandlers
	ip.mu.RUnlock()

	c.index, c.action, c.slotIndex, c.fn = -1, ActionAccessLock, index, f
	c.Next()
	return c.err
}

// Use는 Get/Put/Access/AccessLock 연산 전후에 실행할 미들웨어를 체인에 등록합니다.
// 여러 번 호출하면 등록한 순서대로 체인에 누적되며, nil 핸들러는 실행 시 건너뜁니다.
//
// 미들웨어는 연산을 중단하거나 취소할 수 없습니다. 자세한 내용은 [Context.Next]를 참고하십시오.
func (ip *Pool[T]) Use(handlerFunc ...HandlerFunc[T]) {
	if ip == nil {
		return
	}

	ip.mu.Lock()
	defer ip.mu.Unlock()

	ip.rebuildHandlers(handlerFunc...)
}

// Len은 현재 할당 가능한(사용 중이 아닌) 슬롯 개수를 반환합니다.
// 저장된 개수가 아니라 "남은 여유 개수"입니다.
// 사용 중인 슬롯 수가 필요하면 Cap() - Len()으로 계산하십시오.
func (ip *Pool[T]) Len() int {
	if ip == nil {
		return 0
	}

	return len(ip.q)
}

// Cap은 풀 생성 시 지정한 전체 슬롯 개수를 반환합니다.
func (ip *Pool[T]) Cap() int {
	if ip == nil {
		return 0
	}

	return cap(ip.q)
}

func (ip *Pool[T]) rebuildHandlers(handlerFunc ...HandlerFunc[T]) {
	ip.handlers = append(ip.handlers, handlerFunc...)

	ip.getHandlers = make([]HandlerFunc[T], len(ip.handlers)+1)
	copy(ip.getHandlers, ip.handlers)
	ip.getHandlers[len(ip.handlers)] = func(c *Context[T]) {
		c.slotIndex, c.err = ip.get()
	}

	ip.putHandlers = make([]HandlerFunc[T], len(ip.handlers)+1)
	copy(ip.putHandlers, ip.handlers)
	ip.putHandlers[len(ip.handlers)] = func(c *Context[T]) {
		c.err = ip.put(c.slotIndex)
	}

	ip.accessHandlers = make([]HandlerFunc[T], len(ip.handlers)+1)
	copy(ip.accessHandlers, ip.handlers)
	ip.accessHandlers[len(ip.handlers)] = func(c *Context[T]) {
		c.err = ip.access(c.slotIndex, c.fn)
	}

	ip.accessLockHandlers = make([]HandlerFunc[T], len(ip.handlers)+1)
	copy(ip.accessLockHandlers, ip.handlers)
	ip.accessLockHandlers[len(ip.handlers)] = func(c *Context[T]) {
		c.err = ip.accessLock(c.slotIndex, c.fn)
	}
}

func (ip *Pool[T]) get() (int, error) {
	select {
	case slotIndex := <-ip.q:
		slot := &ip.slots[slotIndex]
		slot.mu.Lock()
		slot.state = StateAlloc
		slot.mu.Unlock()
		return slotIndex, nil
	default:
		return -1, ErrEmpty
	}
}

func (ip *Pool[T]) put(index int) error {
	slot := &ip.slots[index]

	slot.mu.Lock()
	if (slot.state & StateAlloc) != StateAlloc {
		slot.mu.Unlock()
		return ErrNotAllocIndex
	}
	if (slot.state & StateInUse) == StateInUse {
		slot.mu.Unlock()
		return ErrInuseIndex
	}
	slot.state = StateNone

	var zero T
	slot.mem = zero
	slot.mu.Unlock()

	ip.q <- index

	return nil
}

func (ip *Pool[T]) access(index int, fn func(*T)) error {
	slot := &ip.slots[index]

	slot.mu.Lock()
	if (slot.state & StateAlloc) != StateAlloc {
		slot.mu.Unlock()
		return ErrNotAllocIndex
	}
	if (slot.state & StateInUse) == StateInUse {
		slot.mu.Unlock()
		return ErrInuseIndex
	}
	slot.state |= StateInUse
	slot.mu.Unlock()

	// f(&slot.mem)에서 panic 발생 시 복구
	defer func() {
		slot.mu.Lock()
		slot.state &^= StateInUse
		slot.mu.Unlock()
	}()

	fn(&slot.mem)

	return nil
}

func (ip *Pool[T]) accessLock(index int, fn func(*T)) error {
	slot := &ip.slots[index]

	slot.mu.Lock()
	defer slot.mu.Unlock()

	if (slot.state & StateAlloc) != StateAlloc {
		return ErrNotAllocIndex
	}
	if (slot.state & StateInUse) == StateInUse {
		// Access()가 락을 놓고 콜백 실행 중인 경우 도달.
		// 이 시점에는 StateInUse의 소유자가 Access()이므로 절대 해제하면 안 됨.
		return ErrInuseIndex
	}
	slot.state |= StateInUse

	// f(&slot.mem)에서 panic 발생 시, 이 함수가 세운 StateInUse만 복구
	defer func() {
		slot.state &^= StateInUse
	}()

	fn(&slot.mem)

	return nil
}
