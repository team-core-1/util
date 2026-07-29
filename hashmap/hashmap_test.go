package hashmap

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"
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

// 6. 에러 반환 검증
func TestHashMap_Errors(t *testing.T) {
	t.Logf("==================================================")
	t.Logf(" [시험 목적 및 조건]")
	t.Logf("  - 시험 목적 : 정의된 에러(InvalidCap/DupKey/Full/KeyNotFound/CbNil/Closed) 반환 검증")
	t.Logf("  - 시험 조건 : 용량 경계값, 중복 키, 미존재 키, nil 콜백, Close 이후 접근")
	t.Logf("--------------------------------------------------")

	// 6-1. 잘못된 용량
	if _, err := New[int, int](0); err != ErrInvalidCap {
		t.Errorf("New(0): ErrInvalidCap 기대, 실제 %v", err)
	}
	if _, err := New[int, int](-1); err != ErrInvalidCap {
		t.Errorf("New(-1): ErrInvalidCap 기대, 실제 %v", err)
	}

	// 6-2. 중복 키 (맵에 여유가 있는 상태)
	hm, _ := New[int, int](3)
	_ = hm.Put(1, 100)
	if err := hm.Put(1, 200); err != ErrDupKey {
		t.Errorf("중복 Put: ErrDupKey 기대, 실제 %v", err)
	}
	if v, _ := hm.Get(1); v != 100 {
		t.Errorf("중복 Put 실패 후 기존 값이 변경됨: 100 기대, 실제 %d", v)
	}

	// 6-3. 용량 초과
	_ = hm.Put(2, 200)
	_ = hm.Put(3, 300)
	if err := hm.Put(4, 400); err != ErrFull {
		t.Errorf("용량 초과 Put: ErrFull 기대, 실제 %v", err)
	}

	// 6-4. 가득 찬 상태에서의 중복 키는 ErrFull로 보고됨(현재 동작: 용량 검사가 우선)
	if err := hm.Put(1, 999); err != ErrFull {
		t.Errorf("가득 찬 맵의 중복 Put: 현재 동작상 ErrFull 기대, 실제 %v", err)
	}

	// 6-5. 미존재 키
	if _, err := hm.Get(999); err != ErrKeyNotFound {
		t.Errorf("미존재 키 Get: ErrKeyNotFound 기대, 실제 %v", err)
	}
	if _, err := hm.Do(999, func(k, v int) (int, error) { return v, nil }); err != ErrKeyNotFound {
		t.Errorf("미존재 키 Do: ErrKeyNotFound 기대, 실제 %v", err)
	}

	// 6-6. nil 콜백
	if _, err := hm.Do(1, nil); err != ErrCbNil {
		t.Errorf("Do(nil): ErrCbNil 기대, 실제 %v", err)
	}
	if _, err := hm.DoAll(nil); err != ErrCbNil {
		t.Errorf("DoAll(nil): ErrCbNil 기대, 실제 %v", err)
	}

	// 6-7. Close 이후 접근
	hm.Close()
	hm.Close() // 멱등성: 중복 Close에도 패닉이 없어야 함
	hm.Delete(1)

	if err := hm.Put(10, 10); err != ErrClosed {
		t.Errorf("Close 후 Put: ErrClosed 기대, 실제 %v", err)
	}
	if _, err := hm.Get(1); err != ErrClosed {
		t.Errorf("Close 후 Get: ErrClosed 기대, 실제 %v", err)
	}
	if _, err := hm.Do(1, func(k, v int) (int, error) { return v, nil }); err != ErrClosed {
		t.Errorf("Close 후 Do: ErrClosed 기대, 실제 %v", err)
	}
	if _, err := hm.DoAll(func(k, v int) (int, error) { return v, nil }); err != ErrClosed {
		t.Errorf("Close 후 DoAll: ErrClosed 기대, 실제 %v", err)
	}

	count := 0
	for range hm.All() {
		count++
	}
	if count != 0 {
		t.Errorf("Close 후 All: 0개 순회 기대, 실제 %d개", count)
	}

	t.Logf(" [시험 결과] : 정상 (모든 에러 경로 및 Close 멱등성 확인)")
	t.Logf("==================================================")
	record(t, "TestHashMap_Errors", "Verify all defined error paths and Close idempotency")
}

// 7. nil 리시버 안전성 검증
func TestHashMap_NilReceiver(t *testing.T) {
	t.Logf("==================================================")
	t.Logf(" [시험 목적 및 조건]")
	t.Logf("  - 시험 목적 : nil 맵에 대한 모든 메서드 호출이 패닉 없이 ErrNil/제로값을 반환하는지 검증")
	t.Logf("  - 시험 조건 : 초기화하지 않은 *Map 포인터로 전체 공개 메서드 호출")
	t.Logf("--------------------------------------------------")

	var hm *Map[int, string]

	if err := hm.Put(1, "a"); err != ErrNil {
		t.Errorf("nil Put: ErrNil 기대, 실제 %v", err)
	}
	v, err := hm.Get(1)
	if err != ErrNil {
		t.Errorf("nil Get: ErrNil 기대, 실제 %v", err)
	}
	if v != "" {
		t.Errorf("nil Get: 제로값 기대, 실제 %q", v)
	}
	if _, err := hm.Do(1, func(k int, v string) (int, error) { return 0, nil }); err != ErrNil {
		t.Errorf("nil Do: ErrNil 기대, 실제 %v", err)
	}
	if _, err := hm.DoAll(func(k int, v string) (int, error) { return 0, nil }); err != ErrNil {
		t.Errorf("nil DoAll: ErrNil 기대, 실제 %v", err)
	}
	if hm.Len() != 0 {
		t.Errorf("nil Len: 0 기대, 실제 %d", hm.Len())
	}
	if hm.Cap() != 0 {
		t.Errorf("nil Cap: 0 기대, 실제 %d", hm.Cap())
	}

	count := 0
	for range hm.All() {
		count++
	}
	if count != 0 {
		t.Errorf("nil All: 0개 순회 기대, 실제 %d개", count)
	}

	// 반환값이 없는 메서드는 패닉만 발생하지 않으면 정상
	hm.Delete(1)
	hm.Use(func(c *Context[int, string]) {})
	hm.Close()

	t.Logf(" [시험 결과] : 정상 (nil 리시버 전체 메서드가 패닉 없이 방어됨)")
	t.Logf("==================================================")
	record(t, "TestHashMap_NilReceiver", "Verify all methods are nil-receiver safe")
}

// 8. Use 미들웨어 체인 검증
func TestHashMap_Middleware(t *testing.T) {
	t.Logf("==================================================")
	t.Logf(" [시험 목적 및 조건]")
	t.Logf("  - 시험 목적 : Use로 등록한 미들웨어의 실행 순서, Context 접근자, Next 래핑 동작 검증")
	t.Logf("  - 시험 조건 : 미들웨어 2개 등록 후 Put/Get 수행")
	t.Logf("--------------------------------------------------")

	hm, _ := New[int, string](10)

	var order []string
	// mw1은 c.Next()로 후속 핸들러를 감싸고, mw2는 Next()를 호출하지 않음
	hm.Use(func(c *Context[int, string]) {
		order = append(order, "mw1-before")
		c.Next()
		order = append(order, "mw1-after")
	})
	hm.Use(nil) // nil 핸들러는 패닉 없이 건너뛰어야 함
	hm.Use(func(c *Context[int, string]) {
		order = append(order, "mw2")
	})

	// 8-1. Put 경로: 실행 순서 및 Context 접근자
	var putAction ActionType
	var putKey int
	var putValue string
	hm.Use(func(c *Context[int, string]) {
		putAction, putKey, putValue = c.Action(), c.Key(), c.Value()
	})

	if err := hm.Put(7, "seven"); err != nil {
		t.Fatalf("Put 실패: %v", err)
	}

	expectedOrder := []string{"mw1-before", "mw2", "mw1-after"}
	if len(order) != len(expectedOrder) {
		t.Errorf("미들웨어 실행 순서 불일치: %v 기대, 실제 %v", expectedOrder, order)
	} else {
		for i := range expectedOrder {
			if order[i] != expectedOrder[i] {
				t.Errorf("미들웨어 실행 순서 불일치: %v 기대, 실제 %v", expectedOrder, order)
				break
			}
		}
	}
	if putAction != ActionPut {
		t.Errorf("Context.Action(): ActionPut 기대, 실제 %v", putAction)
	}
	if putKey != 7 || putValue != "seven" {
		t.Errorf("Context 접근자: (7, seven) 기대, 실제 (%d, %s)", putKey, putValue)
	}

	// 8-2. 미들웨어가 Next()를 호출하지 않아도 종단 핸들러는 실행됨(중단 불가 설계)
	if v, err := hm.Get(7); err != nil || v != "seven" {
		t.Errorf("미들웨어 통과 후 실제 저장 실패: (seven, nil) 기대, 실제 (%s, %v)", v, err)
	}

	// 8-3. Get 경로: Next() 이후 미들웨어가 조회 결과를 관찰할 수 있어야 함
	var observedValue string
	var observedErr error
	var observedAction ActionType
	hm.Use(func(c *Context[int, string]) {
		c.Next()
		observedAction, observedValue, observedErr = c.Action(), c.Value(), c.Err()
	})

	if _, err := hm.Get(7); err != nil {
		t.Fatalf("Get 실패: %v", err)
	}
	if observedAction != ActionGet {
		t.Errorf("Get Context.Action(): ActionGet 기대, 실제 %v", observedAction)
	}
	if observedValue != "seven" || observedErr != nil {
		t.Errorf("Next() 이후 결과 관찰 실패: (seven, nil) 기대, 실제 (%s, %v)", observedValue, observedErr)
	}

	// 8-4. 실패한 연산의 에러도 미들웨어에서 관찰 가능해야 함
	if _, err := hm.Get(9999); err != ErrKeyNotFound {
		t.Errorf("미존재 키 Get: ErrKeyNotFound 기대, 실제 %v", err)
	}
	if observedErr != ErrKeyNotFound {
		t.Errorf("미들웨어의 에러 관찰 실패: ErrKeyNotFound 기대, 실제 %v", observedErr)
	}

	// 8-5. Delete 경로의 Action 확인
	var deleteAction ActionType
	hm.Use(func(c *Context[int, string]) {
		deleteAction = c.Action()
	})
	hm.Delete(7)
	if deleteAction != ActionDelete {
		t.Errorf("Delete Context.Action(): ActionDelete 기대, 실제 %v", deleteAction)
	}

	t.Logf("--------------------------------------------------")
	t.Logf(" [테스트 수치]")
	t.Logf("  - 미들웨어 실행 순서 : %v", order)
	t.Logf(" [시험 결과] : 정상 (실행 순서/접근자/Next 래핑/nil 핸들러 처리 확인)")
	t.Logf("==================================================")
	record(t, "TestHashMap_Middleware", "Verify Use middleware ordering, context accessors and Next wrapping")
}

// 9. Len/Cap 검증
func TestHashMap_LenCap(t *testing.T) {
	t.Logf("==================================================")
	t.Logf(" [시험 목적 및 조건]")
	t.Logf("  - 시험 목적 : Put/Delete/Close에 따른 Len, Cap 값 변화 검증")
	t.Logf("  - 시험 조건 : Capacity 5로 생성 후 3건 삽입, 1건 삭제, Close 수행")
	t.Logf("--------------------------------------------------")

	hm, _ := New[int, int](5)

	if hm.Cap() != 5 {
		t.Errorf("초기 Cap: 5 기대, 실제 %d", hm.Cap())
	}
	if hm.Len() != 0 {
		t.Errorf("초기 Len: 0 기대, 실제 %d", hm.Len())
	}

	_ = hm.Put(1, 1)
	_ = hm.Put(2, 2)
	_ = hm.Put(3, 3)
	if hm.Len() != 3 {
		t.Errorf("3건 삽입 후 Len: 3 기대, 실제 %d", hm.Len())
	}

	hm.Delete(1)
	if hm.Len() != 2 {
		t.Errorf("1건 삭제 후 Len: 2 기대, 실제 %d", hm.Len())
	}

	// 미존재 키 삭제는 Len에 영향이 없어야 함
	hm.Delete(999)
	if hm.Len() != 2 {
		t.Errorf("미존재 키 삭제 후 Len: 2 기대, 실제 %d", hm.Len())
	}

	// 삭제로 확보된 공간에 재삽입 가능해야 함
	if err := hm.Put(1, 10); err != nil {
		t.Errorf("삭제 후 재삽입 실패: %v", err)
	}

	hm.Close()
	if hm.Len() != 0 {
		t.Errorf("Close 후 Len: 0 기대, 실제 %d", hm.Len())
	}
	// 현재 동작: Close 이후 Cap도 0을 반환
	if hm.Cap() != 0 {
		t.Errorf("Close 후 Cap: 현재 동작상 0 기대, 실제 %d", hm.Cap())
	}

	t.Logf(" [시험 결과] : 정상 (Len/Cap 및 삭제 후 재삽입 동작 확인)")
	t.Logf("==================================================")
	record(t, "TestHashMap_LenCap", "Verify Len and Cap across Put, Delete and Close")
}

// 10. 순회 조기 종료 및 콜백 에러 단축 평가 검증
func TestHashMap_IterationControl(t *testing.T) {
	t.Logf("==================================================")
	t.Logf(" [시험 목적 및 조건]")
	t.Logf("  - 시험 목적 : All 조기 break 시 RLock 해제 여부와 DoAll 에러 발생 시 순회 중단 검증")
	t.Logf("  - 시험 조건 : 5건 삽입 후 첫 요소에서 break, DoAll은 3번째 호출에서 에러 반환")
	t.Logf("--------------------------------------------------")

	hm, _ := New[int, int](10)
	for i := 1; i <= 5; i++ {
		_ = hm.Put(i, i)
	}

	// 10-1. All() 조기 break
	visited := 0
	for range hm.All() {
		visited++
		break
	}
	if visited != 1 {
		t.Errorf("조기 break: 1회 순회 기대, 실제 %d회", visited)
	}

	// break로 순회를 중단해도 내부 RLock이 해제되어야 쓰기가 가능함
	done := make(chan error, 1)
	go func() {
		done <- hm.Put(100, 100)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("조기 break 후 Put 실패: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("데드락: All() 조기 종료 시 RLock이 해제되지 않음")
	}

	// 10-2. DoAll은 콜백 에러 발생 시 즉시 중단
	errStop := errors.New("stop")
	calls := 0
	sum, err := hm.DoAll(func(k, v int) (int, error) {
		calls++
		if calls == 3 {
			return 0, errStop
		}
		return 1, nil
	})
	if !errors.Is(err, errStop) {
		t.Errorf("DoAll 에러 전파 실패: errStop 기대, 실제 %v", err)
	}
	if calls != 3 {
		t.Errorf("DoAll이 에러 이후에도 순회를 계속함: 3회 호출 기대, 실제 %d회", calls)
	}
	if sum != 2 {
		t.Errorf("DoAll 중단 시점까지의 누적합 불일치: 2 기대, 실제 %d", sum)
	}

	t.Logf("--------------------------------------------------")
	t.Logf(" [테스트 수치]")
	t.Logf("  - All 조기 break 순회 횟수 : %d회", visited)
	t.Logf("  - DoAll 콜백 호출 횟수     : %d회 (에러로 중단)", calls)
	t.Logf(" [시험 결과] : 정상 (조기 종료 시 락 해제 및 에러 단축 평가 확인)")
	t.Logf("==================================================")
	record(t, "TestHashMap_IterationControl", "Verify All early-break lock release and DoAll error short-circuit")
}
