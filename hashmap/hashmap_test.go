package hashmap

import (
	"testing"
)

// 1. 기본 CRUD 테스트 (사용자가 락을 관리하는 형태)
func TestHashMap_BasicOps(t *testing.T) {
	hm, _ := New[int, string](10)

	hm.Lock()
	hm.Put(1, "one")
	hm.Unlock()

	hm.RLock()
	val, err := hm.Get(1)
	hm.RUnlock()

	if err != nil || val != "one" {
		t.Errorf("Get failed, got %v, err %v", val, err)
	}
}

// 2. All 메서드 테스트: 클로저를 활용한 외부 변수 수정
func TestHashMap_All(t *testing.T) {
	hm, _ := New[int, int](10)
	hm.Lock()
	hm.Put(1, 100)
	hm.Put(2, 200)
	hm.Unlock()

	total := 0
	hm.RLock()
	// arg로 &total을 전달하고 콜백 내에서 타입 단언하여 수정
	_, err := hm.All(func(k int, v int, arg any) (int, error) {
		sumPtr := arg.(*int)
		*sumPtr += v
		return v, nil
	}, &total)
	hm.RUnlock()

	if err != nil || total != 300 {
		t.Errorf("All failed: total=%d, err=%v", total, err)
	}
}

// 3. Do 메서드 테스트: 특정 키에 대한 콜백 실행
func TestHashMap_Do(t *testing.T) {
	hm, _ := New[int, int](10)
	hm.Lock()
	hm.Put(1, 10)
	hm.Unlock()

	hm.RLock()
	res, err := hm.Do(1, func(k int, v int, arg any) (int, error) {
		return v * 2, nil
	}, nil)
	hm.RUnlock()

	if err != nil || res != 20 {
		t.Errorf("Do failed: res=%d", res)
	}
}

// 4. All 메서드 순회 중 삭제 테스트 (의도적인 동작 검증)
func TestHashMap_AllDelete(t *testing.T) {
	hm, _ := New[int, int](10)
	hm.Lock()
	hm.Put(1, 1)
	hm.Put(2, 2)
	hm.Unlock()

	hm.Lock() // Delete를 하려면 쓰기 락이 필요함
	hm.All(func(k int, v int, arg any) (int, error) {
		if k == 1 {
			hm.Delete(k)
		}
		return 0, nil
	}, nil)
	hm.Unlock()

	hm.RLock()
	_, err := hm.Get(1)
	hm.RUnlock()

	if err != ErrKeyNotFound {
		t.Error("Key 1 should have been deleted")
	}
}
