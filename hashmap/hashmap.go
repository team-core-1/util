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
	sync.RWMutex
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
	if (hm == nil) || (hm.m == nil) {
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

// All은 Map의 모든 키-값 쌍을 순회할 수 있는 반복자를 반환합니다.
// 맵을 순회하는 동안 데이터 일관성을 유지하기 위해 반드시 외부에서 읽기 락을 획득해야 합니다.
//
// 사용 예시:
//
//	hm.Lock() // or hm.RLock()
//	for k, v := range hm.All() {
//		if k == "stop" {
//			break
//		}
//	}
//	hm.Unlock() // or hm.RUnlock()
func (hm *Map[K, V]) All() iter.Seq2[K, V] {
	return func(yield func(K, V) bool) {
		if (hm == nil) || (hm.m == nil) {
			return
		}

		for k, v := range hm.m {
			if !yield(k, v) {
				return
			}
		}
	}
}

func (hm *Map[K, V]) Do(key K, fn func(K, V) (int, error)) (int, error) {
	if hm == nil {
		return 0, ErrNil
	}

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

func (hm *Map[K, V]) DoAll(fn func(K, V) (int, error)) (int, error) {
	if hm == nil {
		return 0, ErrNil
	}

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
	if (hm == nil) || (hm.m == nil) {
		return
	}

	hm.rebuildHandlers(handlerFunc...)
}

func (hm *Map[K, V]) Len() int {
	if (hm == nil) || (hm.m == nil) {
		return 0
	}

	return len(hm.m)
}

func (hm *Map[K, V]) Cap() int {
	if (hm == nil) || (hm.m == nil) {
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
