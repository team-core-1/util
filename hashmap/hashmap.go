package hashmap

import (
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

type HashMap[K comparable, V any] struct {
	sync.RWMutex
	m   map[K]V
	cap int

	pool           sync.Pool
	handlers       []HandlerFunc[K, V]
	putHandlers    []HandlerFunc[K, V]
	getHandlers    []HandlerFunc[K, V]
	deleteHandlers []HandlerFunc[K, V]
}

func New[K comparable, V any](capacity int) (*HashMap[K, V], error) {
	if capacity <= 0 {
		return nil, ErrInvalidCap
	}

	hm := &HashMap[K, V]{
		cap: capacity,
	}

	hm.m = make(map[K]V, capacity)

	hm.pool.New = func() any {
		return &Context[K, V]{}
	}

	hm.rebuildHandlers()

	return hm, nil
}

func (hm *HashMap[K, V]) Close() {
	if hm == nil {
		return
	}

	if hm.m == nil {
		return
	}

	clear(hm.m)

	hm.m = nil
}

func (hm *HashMap[K, V]) Put(key K, value V) error {
	if hm == nil {
		return ErrNil
	}

	if hm.m == nil {
		return ErrClosed
	}

	c := hm.pool.Get().(*Context[K, V])
	c.handlers = hm.putHandlers
	c.index, c.action, c.key, c.value = -1, ActionPut, key, value

	c.Next()
	err := c.err

	c.reset()
	hm.pool.Put(c)

	return err
}

func (hm *HashMap[K, V]) Get(key K) (V, error) {
	var zero V

	if hm == nil {
		return zero, ErrNil
	}

	if hm.m == nil {
		return zero, ErrClosed
	}

	c := hm.pool.Get().(*Context[K, V])
	c.handlers = hm.getHandlers
	c.index, c.action, c.key = -1, ActionGet, key

	c.Next()
	value, err := c.value, c.err

	c.reset()
	hm.pool.Put(c)

	return value, err
}

func (hm *HashMap[K, V]) Delete(key K) {
	if (hm == nil) || (hm.m == nil) {
		return
	}

	c := hm.pool.Get().(*Context[K, V])
	c.handlers = hm.deleteHandlers
	c.index, c.action, c.key = -1, ActionDelete, key

	c.Next()

	c.reset()
	hm.pool.Put(c)
}

func (hm *HashMap[K, V]) All(fn func(K, V) (int, error)) (int, error) {
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
	for key, value := range hm.m {
		res, err := fn(key, value)
		if err != nil {
			return sum, err
		}
		sum += res
	}

	return sum, nil
}

func (hm *HashMap[K, V]) Do(key K, fn func(K, V) (int, error)) (int, error) {
	if hm == nil {
		return 0, ErrNil
	}

	if hm.m == nil {
		return 0, ErrClosed
	}

	if fn == nil {
		return 0, ErrCbNil
	}

	value, ok := hm.m[key]
	if ok {
		return fn(key, value)
	}

	return 0, ErrKeyNotFound
}

func (hm *HashMap[K, V]) Use(handlerFunc ...HandlerFunc[K, V]) {
	if (hm == nil) || (hm.m == nil) {
		return
	}

	hm.rebuildHandlers(handlerFunc...)
}

func (hm *HashMap[K, V]) Len() int {
	if (hm == nil) || (hm.m == nil) {
		return 0
	}

	return len(hm.m)
}

func (hm *HashMap[K, V]) Cap() int {
	if (hm == nil) || (hm.m == nil) {
		return 0
	}

	return hm.cap
}

func (hm *HashMap[K, V]) rebuildHandlers(handlerFunc ...HandlerFunc[K, V]) {
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

func (hm *HashMap[K, V]) put(key K, value V) error {
	if len(hm.m) >= hm.cap {
		return ErrFull
	}

	if _, ok := hm.m[key]; ok {
		return ErrDupKey
	}

	hm.m[key] = value

	return nil
}

func (hm *HashMap[K, V]) get(key K) (V, error) {
	var zero V

	if value, ok := hm.m[key]; ok {
		return value, nil
	}

	return zero, ErrKeyNotFound
}

func (hm *HashMap[K, V]) delete(key K) {
	delete(hm.m, key)
}
