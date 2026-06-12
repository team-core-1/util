package hashmap

import (
	"errors"
	"fmt"
	"maps"
	"sync"
)

var (
	ErrInvalidCap  = errors.New("HashMap fail(invalid capacity)")
	ErrNil         = errors.New("HashMap fail(nil)")
	ErrFull        = errors.New("HashMap fail(full)")
	ErrDup         = errors.New("HashMap fail(key duplicated)")
	ErrKeyNotFound = errors.New("HashMap fail(key not found)")
	ErrCbNil       = errors.New("HashMap fail(callback nil)")
	ErrClosed      = errors.New("HashMap fail(closed)")
)

type HashMap[K comparable, V any] struct {
	sync.RWMutex
	m   map[K]V
	cap int
}

func New[K comparable, V any](capacity int) (*HashMap[K, V], error) {
	if capacity <= 0 {
		return nil, ErrInvalidCap
	}

	m := make(map[K]V, capacity)

	return &HashMap[K, V]{
		m:   m,
		cap: capacity,
	}, nil
}

func (hm *HashMap[K, V]) Close() {
	if hm == nil {
		return
	}

	hm.m = nil
}

func (hm *HashMap[K, V]) Put(key K, value V) error {
	if hm == nil {
		return ErrNil
	}

	if hm.m == nil {
		return ErrClosed
	}

	if len(hm.m) >= hm.cap {
		return ErrFull
	}

	if _, ok := hm.m[key]; ok {
		return ErrDup
	}

	hm.m[key] = value

	return nil
}

func (hm *HashMap[K, V]) Get(key K) (value V, err error) {
	if hm == nil {
		return value, ErrNil
	}

	if hm.m == nil {
		return value, ErrClosed
	}

	if v, ok := hm.m[key]; ok {
		return v, nil
	}

	return value, ErrKeyNotFound
}

func (hm *HashMap[K, V]) Delete(key K) {
	if hm == nil {
		return
	}

	if hm.m == nil {
		return
	}

	delete(hm.m, key)
}

func (hm *HashMap[K, V]) All(f func(K, V, any) (int, error), arg any) (sum int, err error) {
	if hm == nil {
		return 0, ErrNil
	}

	if hm.m == nil {
		return 0, ErrClosed
	}

	if f == nil {
		return 0, ErrCbNil
	}

	for k, v := range hm.m {
		ret, err := f(k, v, arg)
		if err != nil {
			return sum, err
		}
		sum += ret
	}

	return sum, nil
}

func (hm *HashMap[K, V]) Do(key K, f func(K, V, any) (int, error), arg any) (int, error) {
	if hm == nil {
		return 0, ErrNil
	}

	if hm.m == nil {
		return 0, ErrClosed
	}

	if f == nil {
		return 0, ErrCbNil
	}

	value, ok := hm.m[key]
	if ok {
		return f(key, value, arg)
	}

	return 0, ErrKeyNotFound
}

func (hm *HashMap[K, V]) Len() int {
	if hm == nil {
		return 0
	}

	if hm.m == nil {
		return 0
	}

	return len(hm.m)
}

func (hm *HashMap[K, V]) Cap() int {
	if hm == nil {
		return 0
	}

	if hm.m == nil {
		return 0
	}

	return hm.cap
}

func (hm *HashMap[K, V]) AllSafe(f func(K, V, any) (int, error), arg any) (sum int, err error) {
	if hm == nil {
		return 0, ErrNil
	}

	if hm.m == nil {
		return 0, ErrClosed
	}

	if f == nil {
		return 0, ErrCbNil
	}

	defer func() {
		if r := recover(); r != nil {
			sum = 0
			err = fmt.Errorf("HashMap fail(panic: %+v)", r)
		}
	}()

	var snap map[K]V
	func() {
		hm.RLock()
		defer hm.RUnlock()

		snap = make(map[K]V, len(hm.m))
		maps.Copy(snap, hm.m)
	}()

	for k, v := range snap {
		ret, err := f(k, v, arg)
		if err != nil {
			return sum, err
		}
		sum += ret
	}

	return sum, nil
}

func (hm *HashMap[K, V]) DoSafe(key K, f func(K, V, any) (int, error), arg any) (ret int, err error) {
	if hm == nil {
		return 0, ErrNil
	}

	if hm.m == nil {
		return 0, ErrClosed
	}

	if f == nil {
		return 0, ErrCbNil
	}

	defer func() {
		if r := recover(); r != nil {
			ret = 0
			err = fmt.Errorf("HashMap fail(panic: %+v)", r)
		}
	}()

	var snap map[K]V
	func() {
		hm.RLock()
		defer hm.RUnlock()

		snap = make(map[K]V, len(hm.m))
		maps.Copy(snap, hm.m)
	}()

	v, ok := snap[key]
	if ok {
		return f(key, v, arg)
	}

	return 0, ErrKeyNotFound
}
