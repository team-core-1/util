package hashmap

import (
	"errors"
	"sync"
)

var (
	HashMapErrInvalidCapa    = errors.New("HashMap fail(invalid capacity)")
	HashMapErrNil            = errors.New("HashMap fail(nil)")
	HashMapErrPutFull        = errors.New("HashMap Put fail(full)")
	HashMapErrPutDup         = errors.New("HashMap Put fail(duplicated)")
	HashMapErrGetKeyNotFound = errors.New("HashMap Get fail(key not found)")
)

type HashMap[K comparable, V any] struct {
	m        map[K]V
	lock     sync.RWMutex
	capacity uint32
}

func New[K comparable, V any](capacity uint32) (*HashMap[K, V], error) {
	if capacity == 0 {
		return nil, HashMapErrInvalidCapa
	}

	m := make(map[K]V, capacity)

	return &HashMap[K, V]{
		m:        m,
		capacity: capacity,
	}, nil
}

func (hashmap *HashMap[K, V]) Put(key K, value V) error {
	if hashmap == nil {
		return HashMapErrNil
	}

	hashmap.lock.Lock()
	defer hashmap.lock.Unlock()

	// key 중복 여부를 먼저 알려준다.
	if _, ok := hashmap.m[key]; ok {
		return HashMapErrPutDup
	}

	if uint32(len(hashmap.m)) >= hashmap.capacity {
		return HashMapErrPutFull
	}

	hashmap.m[key] = value

	return nil
}

func (hashmap *HashMap[K, V]) Get(key K) (value V, err error) {
	if hashmap == nil {
		return value, HashMapErrNil
	}

	hashmap.lock.RLock()
	defer hashmap.lock.RUnlock()

	if v, ok := hashmap.m[key]; ok {
		return v, nil
	}

	return value, HashMapErrGetKeyNotFound
}

func (hashmap *HashMap[K, V]) Delete(key K) {
	if hashmap == nil {
		return
	}

	hashmap.lock.Lock()
	defer hashmap.lock.Unlock()

	delete(hashmap.m, key)

	return
}

func (hashmap *HashMap[K, V]) All(f func(key K, value V, arg any) int, arg any) (sum int) {
	if (hashmap == nil) || (f == nil) {
		return -1
	}

	ret := 0

	hashmap.lock.RLock()
	defer hashmap.lock.RUnlock()

	for key, value := range hashmap.m {
		if ret = f(key, value, arg); ret < 0 {
			break
		}
		sum += ret
	}

	if ret < 0 {
		return ret
	}

	return sum
}

func (hashmap *HashMap[K, V]) At(key K, f func(key K, value V, arg any) int, arg any) (sum int) {
	if (hashmap == nil) || (f == nil) {
		return -1
	}

	hashmap.lock.RLock()
	defer hashmap.lock.RUnlock()

	value, ok := hashmap.m[key]
	if ok {
		return f(key, value, arg)
	}

	return -1
}
