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
	ActionAll
	ActionDo
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

func (hm *Map[K, V]) Put(key K, value V) error {
	if hm == nil {
		return ErrNil
	}

	hm.mu.Lock()
	defer hm.mu.Unlock()

	if hm.m == nil {
		return ErrClosed
	}

	c := hm.pool.Get().(*Context[K, V])
	defer func() {
		c.reset()
		hm.pool.Put(c)
	}()

	c.handlers = hm.putHandlers

	c.index, c.action, c.key, c.value = -1, ActionPut, key, value
	c.Next()
	return c.err
}

func (hm *Map[K, V]) Get(key K) (V, error) {
	var zero V

	if hm == nil {
		return zero, ErrNil
	}

	hm.mu.RLock()
	defer hm.mu.RUnlock()

	if hm.m == nil {
		return zero, ErrClosed
	}

	c := hm.pool.Get().(*Context[K, V])
	defer func() {
		c.reset()
		hm.pool.Put(c)
	}()

	c.handlers = hm.getHandlers

	c.index, c.action, c.key = -1, ActionGet, key
	c.Next()
	return c.value, c.err
}

func (hm *Map[K, V]) Delete(key K) {
	if hm == nil {
		return
	}

	hm.mu.Lock()
	defer hm.mu.Unlock()

	if hm.m == nil {
		return
	}

	c := hm.pool.Get().(*Context[K, V])
	defer func() {
		c.reset()
		hm.pool.Put(c)
	}()

	c.handlers = hm.deleteHandlers

	c.index, c.action, c.key = -1, ActionDelete, key
	c.Next()
}

// All은 Map의 모든 키-값 쌍을 순회할 수 있는 반복자(iter.Seq2)를 반환합니다.
//
// [동시성 및 주의사항]
//   - 순회하는 동안 내부적으로 읽기 락(RLock)을 유지합니다.
//   - 순회 루프(for-range) 내부에서 Put(), Delete(), Close() 등의 쓰기 메서드를 직접 호출하면
//     동일 고루틴에서 데드락(Self-Deadlock)이 발생하므로 절대로 직접 호출하지 마십시오.
//   - 순회 중 요소를 삭제하거나 수정해야 하는 경우, 아래 예시와 같이 대상 키를 별도 슬라이스에
//     수집한 후 순회 루프 밖에서 처리하십시오.
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

// Do는 지정한 키(key)가 존재할 경우, 해당 키와 값을 인자로 전달하여 콜백 함수(fn)를 실행합니다.
//
// [동시성 및 주의사항]
// - 콜백 함수가 실행되는 동안 읽기 락(RLock)이 유지됩니다.
// - 콜백 함수(fn) 내부에서 Map의 쓰기 메서드(Put, Delete, Close 등)를 호출하지 마십시오. (데드락 위험)
// - 콜백 내부에서 시간 복잡도가 높거나 I/O 대기가 발생하는 무거운 작업을 지양하십시오.
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
//
// [동시성 및 주의사항]
// - 순회하는 동안 내부적으로 읽기 락(RLock)을 유지합니다.
// - 콜백 함수(fn) 내부에서 Map의 쓰기 메서드(Put, Delete, Close 등)를 호출하지 마십시오. (데드락 위험)
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

func (hm *Map[K, V]) Use(handlerFunc ...HandlerFunc[K, V]) {
	if hm == nil {
		return
	}

	hm.mu.Lock()
	defer hm.mu.Unlock()

	if hm.m == nil {
		return
	}

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

	if value, ok := hm.m[key]; ok {
		return value, nil
	}

	return zero, ErrKeyNotFound
}

func (hm *Map[K, V]) delete(key K) {
	delete(hm.m, key)
}
