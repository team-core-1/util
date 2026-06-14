package hashmap

import (
	"sync"
	"testing"
)

func TestHashMap_Stress(t *testing.T) {
	// 1. cap이 1인 HashMap 생성
	hm, _ := New[int, int](1)
	var wg sync.WaitGroup

	// 2. 멀티 고루틴: Put, Get, Delete 동시 시도
	for i := 0; i < 10; i++ {
		wg.Add(3)
		go func(val int) {
			defer wg.Done()
			hm.Lock()
			_ = hm.Put(val, val)
			hm.Unlock()
		}(i)

		go func(val int) {
			defer wg.Done()
			hm.RLock()
			_, _ = hm.Get(val)
			hm.RUnlock()
		}(i)

		go func(val int) {
			defer wg.Done()
			hm.Lock()
			hm.Delete(val)
			hm.Unlock()
		}(i)
	}

	// 3. 멀티 고루틴: All, Do 시도 (callback에서 Put, Get, Delete 시도)
	wg.Add(2)
	go func() {
		defer wg.Done()
		hm.Lock() // 순회 중 수정이 포함되므로 쓰기 락
		_, _ = hm.All(func(k, v int, arg any) (int, error) {
			hm.Put(k+100, v)
			_, _ = hm.Get(k)
			hm.Delete(k)
			return 0, nil
		}, nil)
		hm.Unlock()
	}()

	go func() {
		defer wg.Done()
		hm.RLock()
		_, _ = hm.Do(1, func(k, v int, arg any) (int, error) {
			return v, nil
		}, nil)
		hm.RUnlock()
	}()

	// 4. 멀티 고루틴에서 Close 실행
	wg.Add(1)
	go func() {
		defer wg.Done()
		hm.Lock()
		hm.Close()
		hm.Unlock()
	}()

	// 5. 고루틴에서 계속 동작 시도 (Close 이후 에러 반환 확인)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(val int) {
			defer wg.Done()
			hm.Lock()
			err := hm.Put(val, val)
			hm.Unlock()
			if err != nil && err != ErrClosed && err != ErrFull {
				t.Errorf("Unexpected error after close: %v", err)
			}
		}(i)
	}

	wg.Wait()
	// 6. 결과 확인
	t.Logf("Stress test finished. Map state: %v", hm.m)
}
