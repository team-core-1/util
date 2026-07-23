package hashmap

import (
	"fmt"
	"os"
	"sync"
	"testing"
)

var (
	summaryMu     sync.Mutex
	testSummaries []string
)

func record(t *testing.T, name string, detail string) {
	status := "PASS"
	if t.Failed() {
		status = "FAIL"
	}
	summaryMu.Lock()
	defer summaryMu.Unlock()
	testSummaries = append(testSummaries, fmt.Sprintf("[%s] %-35s | %s", status, name, detail))
}

func TestMain(m *testing.M) {
	exitCode := m.Run()
	fmt.Println("\n==========================================================================================")
	fmt.Println("                                  ALL TESTS SUMMARY REPORT")
	fmt.Println("==========================================================================================")
	for _, s := range testSummaries {
		fmt.Println(s)
	}
	fmt.Println("==========================================================================================")
	os.Exit(exitCode)
}

func TestHashMap(t *testing.T) {
	// 1. Basic CRUD Test
	t.Run("BasicOps", func(t *testing.T) {
		t.Logf("==================================================")
		t.Logf(" [시험 목적 및 조건]")
		t.Logf("  - 시험 목적 : HashMap의 기본 CRUD(Put, Get) 동작 검증")
		t.Logf("  - 시험 조건 : Capacity: 10, 단일 고루틴, 내부 Lock 사용")
		t.Logf("--------------------------------------------------")

		hm, _ := New[int, string](10)

		err := hm.Put(1, "one")
		if err != nil {
			t.Fatalf("Put failed: %v", err)
		}

		val, err := hm.Get(1)

		if err != nil {
			t.Errorf("Get failed: %v", err)
		}
		if val != "one" {
			t.Errorf("Get value mismatch: expected 'one', got '%v'", val)
		}

		t.Logf(" [시험 결과] : 정상 (Put한 값이 정상적으로 Get됨)")
		t.Logf("==================================================")
		record(t, "TestHashMap/BasicOps", "Verify basic Put and Get operations")
	})

	// 2. All Method Test (iter.Seq2)
	t.Run("All", func(t *testing.T) {
		t.Logf("==================================================")
		t.Logf(" [시험 목적 및 조건]")
		t.Logf("  - 시험 목적 : All 메서드(iter.Seq2)를 이용한 맵 전체 요소 순회 검증")
		t.Logf("  - 시험 조건 : 2개의 데이터 삽입 후 for-range 순회하며 값 누적")
		t.Logf("--------------------------------------------------")

		hm, _ := New[int, int](10)
		_ = hm.Put(1, 100)
		_ = hm.Put(2, 200)

		total := 0
		for _, v := range hm.All() {
			total += v
		}

		if total != 300 {
			t.Errorf("All failed: expected total 300, got %d", total)
		}

		t.Logf(" [시험 결과] : 정상 (모든 요소를 for-range 순회하여 누적합 300 달성)")
		t.Logf("==================================================")
		record(t, "TestHashMap/All", "Verify All iterator method iterating all key-values")
	})

	// 2-1. DoAll Method Test (Callback)
	t.Run("DoAll", func(t *testing.T) {
		t.Logf("==================================================")
		t.Logf(" [시험 목적 및 조건]")
		t.Logf("  - 시험 목적 : DoAll 메서드(콜백)를 이용한 맵 전체 요소 순회 검증")
		t.Logf("  - 시험 조건 : 2개의 데이터 삽입 후 콜백 순회하며 값 누적")
		t.Logf("--------------------------------------------------")

		hm, _ := New[int, int](10)
		_ = hm.Put(1, 100)
		_ = hm.Put(2, 200)

		total := 0
		_, err := hm.DoAll(func(k int, v int) (int, error) {
			total += v
			return v, nil
		})

		if err != nil {
			t.Errorf("DoAll failed with error: %v", err)
		}
		if total != 300 {
			t.Errorf("DoAll failed: expected total 300, got %d", total)
		}

		t.Logf(" [시험 결과] : 정상 (모든 요소를 콜백 순회하여 누적합 300 달성)")
		t.Logf("==================================================")
		record(t, "TestHashMap/DoAll", "Verify DoAll callback method iterating all key-values")
	})

	// 3. Do Method Test
	t.Run("Do", func(t *testing.T) {
		t.Logf("==================================================")
		t.Logf(" [시험 목적 및 조건]")
		t.Logf("  - 시험 목적 : Do 메서드를 활용하여 특정 키 요소만 안전하게 다루기")
		t.Logf("  - 시험 조건 : Key 1에 대해 콜백 함수 실행하여 연산")
		t.Logf("--------------------------------------------------")

		hm, _ := New[int, int](10)
		_ = hm.Put(1, 10)

		res, err := hm.Do(1, func(k int, v int) (int, error) {
			return v * 2, nil
		})

		if err != nil {
			t.Errorf("Do failed with error: %v", err)
		}
		if res != 20 {
			t.Errorf("Do failed: expected 20, got %d", res)
		}

		t.Logf(" [시험 결과] : 정상 (특정 키 콜백이 수행되어 값 2배 연산 완료)")
		t.Logf("==================================================")
		record(t, "TestHashMap/Do", "Verify Do method executing callback on specific key")
	})

	// 4. Delete while Iterating Test
	t.Run("AllDelete", func(t *testing.T) {
		t.Logf("==================================================")
		t.Logf(" [시험 목적 및 조건]")
		t.Logf("  - 시험 목적 : All iterator 순회 후 수집된 키를 안전하게 삭제하는 패턴 검증")
		t.Logf("  - 시험 조건 : Key 1과 2를 넣고 순회 후 Key 1 삭제")
		t.Logf("--------------------------------------------------")

		hm, _ := New[int, int](10)
		_ = hm.Put(1, 1)
		_ = hm.Put(2, 2)

		var toDelete []int
		for k := range hm.All() {
			if k == 1 {
				toDelete = append(toDelete, k)
			}
		}
		for _, k := range toDelete {
			hm.Delete(k)
		}

		_, err := hm.Get(1)

		if err != ErrKeyNotFound {
			t.Errorf("Key 1 should have been deleted, but got err: %v", err)
		}

		t.Logf(" [시험 결과] : 정상 (순회 후 수집된 Key 1 삭제 및 확인 완료)")
		t.Logf("==================================================")
		record(t, "TestHashMap/AllDelete", "Verify deleting keys after collecting from All iterator method")
	})

	// 5. Concurrent Stress Test
	t.Run("Stress", func(t *testing.T) {
		t.Logf("==================================================")
		t.Logf(" [시험 목적 및 조건]")
		t.Logf("  - 시험 목적 : 멀티 고루틴 환경에서 Put/Get/Delete/All/Do/Close 경합 동작 검증")
		t.Logf("  - 시험 조건 : Capacity: 1, 10개 고루틴 그룹 경합 및 중간 Close() 수행")
		t.Logf("--------------------------------------------------")

		hm, _ := New[int, int](1)
		var wg sync.WaitGroup

		for i := 0; i < 10; i++ {
			wg.Add(3)
			go func(val int) {
				defer wg.Done()
				_ = hm.Put(val, val)
			}(i)

			go func(val int) {
				defer wg.Done()
				_, _ = hm.Get(val)
			}(i)

			go func(val int) {
				defer wg.Done()
				hm.Delete(val)
			}(i)
		}

		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = hm.DoAll(func(k, v int) (int, error) {
				return 0, nil
			})
		}()

		go func() {
			defer wg.Done()
			_, _ = hm.Do(1, func(k, v int) (int, error) {
				return v, nil
			})
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			hm.Close()
		}()

		for i := 0; i < 5; i++ {
			wg.Add(1)
			go func(val int) {
				defer wg.Done()
				err := hm.Put(val, val)
				if err != nil && err != ErrClosed && err != ErrFull {
					t.Errorf("Unexpected error after close: %v", err)
				}
			}(i)
		}

		wg.Wait()
		t.Logf(" [시험 결과] : 정상 (데드락이나 패닉 없이 멀티 고루틴 연산 완료)")
		t.Logf("==================================================")
		record(t, "TestHashMap/Stress", "Verify map integrity and safety under concurrent load with close")
	})
}
