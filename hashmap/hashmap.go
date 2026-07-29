package hashmap

import (
	"iter"
	"sync"
)

type ErrorType string

func (e ErrorType) Error() string {
	return string(e)
}

const (
	ErrInvalidCap  = ErrorType("hashmap: invalid capacity")
	ErrNil         = ErrorType("hashmap: map is nil")
	ErrFull        = ErrorType("hashmap: map is full")
	ErrDupKey      = ErrorType("hashmap: duplicated key")
	ErrKeyNotFound = ErrorType("hashmap: key not found")
	ErrCbNil       = ErrorType("hashmap: callback nil")
	ErrClosed      = ErrorType("hashmap: map is closed")
)

type ActionType int

const (
	ActionPut ActionType = iota + 1
	ActionGet
	ActionDelete
)

type HandlerFunc[K comparable, V any] func(*Context[K, V])

type Map[K comparable, V any] struct {
	mu  sync.RWMutex
	m   map[K]V
	cap int

	pool           sync.Pool
	handlers       []HandlerFunc[K, V]
	putHandlers    []HandlerFunc[K, V]
	getHandlers    []HandlerFunc[K, V]
	deleteHandlers []HandlerFunc[K, V]
}

func New[K comparable, V any](capacity int) (*Map[K, V], error) {
	if capacity <= 0 {
		return nil, ErrInvalidCap
	}

	hm := &Map[K, V]{
		cap: capacity,
	}

	hm.m = make(map[K]V, capacity)

	hm.pool.New = func() any {
		return &Context[K, V]{}
	}

	hm.rebuildHandlers()

	return hm, nil
}

func (hm *Map[K, V]) Close() {
	if hm == nil {
		return
	}

	hm.mu.Lock()
	defer hm.mu.Unlock()

	if hm.m == nil {
		return
	}

	clear(hm.m)

	hm.m = nil
}

// Put은 키-값 쌍을 새로 저장합니다. 신규 삽입 전용이며 기존 값 갱신은 지원하지 않습니다.
//   - 이미 존재하는 키면 ErrDupKey를 반환하고 기존 값은 그대로 유지됩니다.
//   - 저장된 값을 바꾸려면 Delete 후 Put을 사용하십시오.
//     (두 연산 사이에 다른 고루틴이 개입할 수 있으므로 원자적이지 않습니다.)
//   - 저장 개수가 용량에 도달하면 ErrFull을 반환합니다.
//     용량 검사가 중복 키 검사보다 먼저 수행되므로, 가득 찬 맵에 기존 키를 Put하면
//     ErrDupKey가 아니라 ErrFull이 반환됩니다.
func (hm *Map[K, V]) Put(key K, value V) error {
	if hm == nil {
		return ErrNil
	}

	c := hm.pool.Get().(*Context[K, V])
	defer func() {
		c.reset()
		hm.pool.Put(c)
	}()

	hm.mu.RLock()
	c.handlers = hm.putHandlers
	hm.mu.RUnlock()

	c.index, c.action, c.key, c.value = -1, ActionPut, key, value
	c.Next()
	return c.err
}

func (hm *Map[K, V]) Get(key K) (V, error) {
	var zero V

	if hm == nil {
		return zero, ErrNil
	}

	c := hm.pool.Get().(*Context[K, V])
	defer func() {
		c.reset()
		hm.pool.Put(c)
	}()

	hm.mu.RLock()
	c.handlers = hm.getHandlers
	hm.mu.RUnlock()

	c.index, c.action, c.key = -1, ActionGet, key
	c.Next()
	return c.value, c.err
}

func (hm *Map[K, V]) Delete(key K) {
	if hm == nil {
		return
	}

	c := hm.pool.Get().(*Context[K, V])
	defer func() {
		c.reset()
		hm.pool.Put(c)
	}()

	hm.mu.RLock()
	c.handlers = hm.deleteHandlers
	hm.mu.RUnlock()

	c.index, c.action, c.key = -1, ActionDelete, key
	c.Next()
}

// All은 Map의 모든 키-값 쌍을 순회할 수 있는 반복자(iter.Seq2)를 반환합니다.
//
// [동시성 및 주의사항]
//   - 순회하는 동안 내부적으로 읽기 락(RLock)을 유지합니다.
//   - 순회 루프(for-range) 내부에서는 Map의 어떤 메서드도 호출하지 마십시오.
//     쓰기 메서드(Put/Delete/Close)뿐 아니라 읽기 메서드(Get/Len/Cap/Do/DoAll/All)도 금지입니다.
//     자세한 이유는 아래 [읽기 메서드도 금지인 이유]를 참고하십시오.
//   - 순회 중 요소를 삭제하거나 수정해야 하는 경우, 아래 예시와 같이 대상 키를 별도 슬라이스에
//     수집한 후 순회 루프 밖에서 처리하십시오.
//   - 순회로 전달되는 V는 값 복사본입니다. 여기서 수정해도 Map에 반영되지 않습니다.
//
// [읽기 메서드도 금지인 이유]
// sync.RWMutex는 재귀적인 읽기 잠금을 보장하지 않습니다.
// 이미 RLock을 쥔 상태에서 다시 RLock을 요청할 때 다른 고루틴이 쓰기 락을 대기 중이면,
// 뒤따르는 RLock이 그 쓰기 대기자보다 뒤로 밀려 영구히 차단됩니다.
// 즉 아래와 같은 코드는 동시에 Put이 호출되는 순간 데드락에 빠집니다.
//
//	for k, v := range hm.All() {
//		other, _ := hm.Get(k + 1) // 금지: 콜백 내부의 재귀적 읽기 잠금
//		_ = other
//	}
//
// [사용 예시]
//
//	// 1. 일반적인 순회
//	for k, v := range hm.All() {
//		fmt.Println(k, v)
//	}
//
//	// 2. 순회 중 조건부 삭제 (안전한 패턴)
//	var toDelete []K
//	for k, v := range hm.All() {
//		if shouldDelete(k, v) {
//			toDelete = append(toDelete, k)
//		}
//	}
//	for _, k := range toDelete {
//		hm.Delete(k) // 순회 외부에서 삭제
//	}
func (hm *Map[K, V]) All() iter.Seq2[K, V] {
	return func(yield func(K, V) bool) {
		if hm == nil {
			return
		}

		hm.mu.RLock()
		defer hm.mu.RUnlock()

		for k, v := range hm.m {
			if !yield(k, v) {
				return
			}
		}
	}
}

// Do는 지정한 키(key)가 존재할 경우, 해당 키와 값을 인자로 전달하여 콜백 함수(fn)를 실행하고
// 콜백의 반환값을 그대로 전달합니다.
// 키가 없으면 ErrKeyNotFound, 콜백이 nil이면 ErrCbNil, Close된 맵이면 ErrClosed를 반환합니다.
//
// 반환 타입 int는 콜백이 산출한 임의의 정수 결과를 호출 측으로 전달하기 위한 것입니다.
// (DoAll은 이 값을 누적하여 합계를 반환합니다. 값이 필요 없으면 0을 반환하면 됩니다.)
//
// [동시성 및 주의사항]
//   - 콜백 함수가 실행되는 동안 읽기 락(RLock)이 유지됩니다.
//   - 콜백 함수(fn) 내부에서는 Map의 어떤 메서드도 호출하지 마십시오.
//     쓰기 메서드(Put/Delete/Close)뿐 아니라 읽기 메서드(Get/Len/Cap/Do/DoAll/All)도 금지입니다.
//     sync.RWMutex는 재귀적 읽기 잠금을 보장하지 않으므로, 다른 고루틴이 쓰기 락을 대기 중이면
//     콜백 내부의 읽기 요청이 영구히 차단되어 데드락에 빠집니다. (자세한 설명은 All 참고)
//   - 콜백 내부에서 시간 복잡도가 높거나 I/O 대기가 발생하는 무거운 작업을 지양하십시오.
//     콜백이 길어질수록 다른 고루틴의 쓰기 연산이 모두 대기합니다.
//   - 콜백에 전달되는 V는 값 복사본입니다. 여기서 수정해도 Map에 반영되지 않습니다.
//     저장된 값을 바꾸려면 Delete 후 Put을 사용하십시오. (제자리 갱신은 지원하지 않습니다.)
//
// [사용 예시]
//
//	res, err := hm.Do("user1", func(k string, v User) (int, error) {
//		return v.Age, nil
//	})
func (hm *Map[K, V]) Do(key K, fn func(K, V) (int, error)) (int, error) {
	if hm == nil {
		return 0, ErrNil
	}

	hm.mu.RLock()
	defer hm.mu.RUnlock()

	if hm.m == nil {
		return 0, ErrClosed
	}

	if fn == nil {
		return 0, ErrCbNil
	}

	v, ok := hm.m[key]
	if ok {
		return fn(key, v)
	}

	return 0, ErrKeyNotFound
}

// DoAll은 Map의 모든 키-값 쌍을 순회하며 콜백 함수(fn)를 실행하고 연산 결과의 합을 반환합니다.
// 콜백 함수에서 에러가 발생하면 순회를 즉시 중단하고 해당 시점까지의 합계와 에러를 반환합니다.
// 합계가 필요 없으면 콜백에서 0을 반환하면 됩니다.
//
// [동시성 및 주의사항]
//   - 순회하는 동안 내부적으로 읽기 락(RLock)을 유지합니다.
//   - 콜백 함수(fn) 내부에서는 Map의 어떤 메서드도 호출하지 마십시오.
//     쓰기 메서드(Put/Delete/Close)뿐 아니라 읽기 메서드(Get/Len/Cap/Do/DoAll/All)도 금지입니다.
//     sync.RWMutex는 재귀적 읽기 잠금을 보장하지 않으므로, 다른 고루틴이 쓰기 락을 대기 중이면
//     콜백 내부의 읽기 요청이 영구히 차단되어 데드락에 빠집니다. (자세한 설명은 All 참고)
//   - 순회 순서는 Go 맵의 특성상 매번 달라집니다.
//   - 콜백에 전달되는 V는 값 복사본입니다. 여기서 수정해도 Map에 반영되지 않습니다.
//
// [사용 예시]
//
//	totalSum, err := hm.DoAll(func(k string, v int) (int, error) {
//		return v, nil
//	})
func (hm *Map[K, V]) DoAll(fn func(K, V) (int, error)) (int, error) {
	if hm == nil {
		return 0, ErrNil
	}

	hm.mu.RLock()
	defer hm.mu.RUnlock()

	if hm.m == nil {
		return 0, ErrClosed
	}

	if fn == nil {
		return 0, ErrCbNil
	}

	sum := 0
	for k, v := range hm.m {
		res, err := fn(k, v)
		if err != nil {
			return sum, err
		}
		sum += res
	}

	return sum, nil
}

// Use는 Put/Get/Delete 연산 전후에 실행할 미들웨어를 체인에 등록합니다.
// 여러 번 호출하면 등록한 순서대로 체인에 누적되며, nil 핸들러는 실행 시 건너뜁니다.
//
// 미들웨어는 연산을 중단하거나 취소할 수 없습니다. 자세한 내용은 [Context.Next]를 참고하십시오.
func (hm *Map[K, V]) Use(handlerFunc ...HandlerFunc[K, V]) {
	if hm == nil {
		return
	}

	hm.mu.Lock()
	defer hm.mu.Unlock()

	hm.rebuildHandlers(handlerFunc...)
}

func (hm *Map[K, V]) Len() int {
	if hm == nil {
		return 0
	}

	hm.mu.RLock()
	defer hm.mu.RUnlock()

	if hm.m == nil {
		return 0
	}

	return len(hm.m)
}

func (hm *Map[K, V]) Cap() int {
	if hm == nil {
		return 0
	}

	hm.mu.RLock()
	defer hm.mu.RUnlock()

	if hm.m == nil {
		return 0
	}

	return hm.cap
}

func (hm *Map[K, V]) rebuildHandlers(handlerFunc ...HandlerFunc[K, V]) {
	hm.handlers = append(hm.handlers, handlerFunc...)

	hm.putHandlers = make([]HandlerFunc[K, V], len(hm.handlers)+1)
	copy(hm.putHandlers, hm.handlers)
	hm.putHandlers[len(hm.handlers)] = func(c *Context[K, V]) {
		c.err = hm.put(c.key, c.value)
	}

	hm.getHandlers = make([]HandlerFunc[K, V], len(hm.handlers)+1)
	copy(hm.getHandlers, hm.handlers)
	hm.getHandlers[len(hm.handlers)] = func(c *Context[K, V]) {
		c.value, c.err = hm.get(c.key)
	}

	hm.deleteHandlers = make([]HandlerFunc[K, V], len(hm.handlers)+1)
	copy(hm.deleteHandlers, hm.handlers)
	hm.deleteHandlers[len(hm.handlers)] = func(c *Context[K, V]) {
		hm.delete(c.key)
	}
}

func (hm *Map[K, V]) put(key K, value V) error {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	if hm.m == nil {
		return ErrClosed
	}

	if len(hm.m) >= hm.cap {
		return ErrFull
	}

	if _, ok := hm.m[key]; ok {
		return ErrDupKey
	}

	hm.m[key] = value

	return nil
}

func (hm *Map[K, V]) get(key K) (V, error) {
	var zero V

	hm.mu.RLock()
	defer hm.mu.RUnlock()

	if hm.m == nil {
		return zero, ErrClosed
	}

	if value, ok := hm.m[key]; ok {
		return value, nil
	}

	return zero, ErrKeyNotFound
}

func (hm *Map[K, V]) delete(key K) {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	if hm.m == nil {
		return
	}

	delete(hm.m, key)
}
