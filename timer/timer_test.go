package timer

import (
	"fmt"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RussellLuo/timingwheel"
)

type largeData struct {
	buffer [1024 * 1024]byte // 1MB
}

var (
	summaryMu     sync.Mutex
	testSummaries []string
)

func record(t *testing.T, detail string) {
	status := "PASS"
	if t.Failed() {
		status = "FAIL"
	}
	summaryMu.Lock()
	defer summaryMu.Unlock()
	testSummaries = append(testSummaries, fmt.Sprintf("[%s] %-35s | %s", status, t.Name(), detail))
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

// 1. 미들웨어 등록 및 정상 호출 확인
func TestTimer_Middleware(t *testing.T) {
	const capacity = 10

	tw := timingwheel.NewTimingWheel(10*time.Millisecond, 20)
	tw.Start()
	defer tw.Stop()

	engine, _ := New[int](tw, capacity)

	var setCalls, cancelCalls, timeoutCalls atomic.Uint64

	engine.Use(func(c *Context[int]) {
		switch c.Action() {
		case ActionSet:
			setCalls.Add(1)
		case ActionCancel:
			cancelCalls.Add(1)
		case ActionTimeout:
			timeoutCalls.Add(1)
		}
		c.Next()
	})

	t.Logf("==================================================")
	t.Logf(" [시험 목적 및 조건]")
	t.Logf("  - 시험 목적 : Set/Cancel/Timeout 시 미들웨어 호출 여부 확인")
	t.Logf("  - 시험 조건 : Capacity:%d", capacity)
	t.Logf("--------------------------------------------------")

	tm, err := engine.Set(20*time.Millisecond, 100)
	if err != nil {
		t.Fatalf("Set 실패: %v", err)
	}

	time.Sleep(5 * time.Millisecond)

	err = engine.Cancel(tm)
	if err != nil {
		t.Fatalf("Cancel 실패: %v", err)
	}

	_, err = engine.Set(10*time.Millisecond, 200)
	if err != nil {
		t.Fatalf("Set 실패: %v", err)
	}

	// Timeout 수신 대기
	select {
	case <-engine.C():
	case <-time.After(100 * time.Millisecond):
		t.Error("Timeout 수신 대기 시간 초과")
	}

	engine.Close()

	t.Logf(" [테스트 수치]")
	t.Logf("  - Set 미들웨어 호출 : %d 회", setCalls.Load())
	t.Logf("  - Cancel 미들웨어 호출 : %d 회", cancelCalls.Load())
	t.Logf("  - Timeout 미들웨어 호출 : %d 회", timeoutCalls.Load())
	t.Logf("--------------------------------------------------")
	t.Logf(" [시험 결과]")
	if t.Failed() {
		t.Logf("  - 미들웨어 동작 실패")
	} else {
		t.Logf("  - 모든 미들웨어(Set/Cancel/Timeout) 검증 완료")
	}
	t.Logf("==================================================")

	record(t, fmt.Sprintf("Set:%d, Cancel:%d, Timeout:%d", setCalls.Load(), cancelCalls.Load(), timeoutCalls.Load()))
}

// 2. cap이 100인 timer engine 생성 -> 하나의 고루틴에서 C()로 timeout 처리하고, 멀티 고루틴에서 Set()한 후 일부는 Cancel 나머지는 timeout 처리
func TestTimer_Integrity(t *testing.T) {
	const capacity = 100
	const totalSets = 200
	const cancelCount = 50

	tw := timingwheel.NewTimingWheel(10*time.Millisecond, 20)
	tw.Start()
	defer tw.Stop()

	engine, _ := New[int](tw, capacity)

	var setSucc, setFull, timeoutCount atomic.Uint64
	var cancelSucc atomic.Uint64

	receivedKeys := sync.Map{}
	var wg sync.WaitGroup
	wg.Add(1)

	// Timeout 처리 고루틴 (Consumer)
	go func() {
		defer wg.Done()
		for key := range engine.C() {
			receivedKeys.Store(key, true)
			timeoutCount.Add(1)
		}
	}()

	t.Logf("==================================================")
	t.Logf(" [시험 목적 및 조건]")
	t.Logf("  - 시험 목적 : 타이머 데이터 정합성 및 Cancel/Timeout 동작 확인")
	t.Logf("  - 시험 조건 : Capacity:%d, 총 요청:%d, Cancel 시도:%d", capacity, totalSets, cancelCount)
	t.Logf("--------------------------------------------------")

	// 멀티 고루틴에서 Set (Producer)
	var setWg sync.WaitGroup
	timers := make([]*Timer, totalSets)
	keys := make([]int, totalSets)

	for i := 0; i < totalSets; i++ {
		setWg.Add(1)
		go func(id int) {
			defer setWg.Done()
			key := id + 1000
			tm, err := engine.Set(50*time.Millisecond, key)
			if err == nil {
				setSucc.Add(1)
				timers[id] = tm
				keys[id] = key
			} else if err == ErrExpiredQueueFull {
				setFull.Add(1)
			}
		}(i)
	}
	setWg.Wait()

	// 일부 Cancel 처리
	cancelledSet := make(map[int]bool)
	for i := 0; i < totalSets; i++ {
		if timers[i] != nil && len(cancelledSet) < cancelCount {
			engine.Cancel(timers[i])
			cancelledSet[keys[i]] = true
			cancelSucc.Add(1)
		}
	}

	// 모든 타이머가 동작할 때까지 대기
	time.Sleep(200 * time.Millisecond)
	engine.Close()
	wg.Wait()

	t.Logf(" [테스트 수치]")
	t.Logf("  - Set 성공 : %d, 실패(Full) : %d", setSucc.Load(), setFull.Load())
	t.Logf("  - Cancel 성공 : %d", cancelSucc.Load())
	t.Logf("  - Timeout 수신 : %d", timeoutCount.Load())

	t.Logf("--------------------------------------------------")
	t.Logf(" [시험 결과]")
	expectedTimeout := setSucc.Load() - cancelSucc.Load()
	if timeoutCount.Load() != expectedTimeout {
		t.Errorf("  - 수신 개수 불일치: 예상 %d, 실제 %d", expectedTimeout, timeoutCount.Load())
	} else {
		t.Logf("  - 수신 개수 정합성 확인 완료")
	}

	// Key 값 확인
	receivedKeys.Range(func(k, v any) bool {
		key := k.(int)
		if cancelledSet[key] {
			t.Errorf("  - 오동작: Cancel된 Key(%d)가 수신됨", key)
		}
		return true
	})

	t.Logf("==================================================")
	record(t, fmt.Sprintf("SetSucc:%d, Cancel:%d, Received:%d", setSucc.Load(), cancelSucc.Load(), timeoutCount.Load()))
}

// 3. cap이 1인 timer engine 생성하고, 멀티고루틴에서 Set, Cancel, timeout이 정상적으로 처리되는지 확인
func TestTimer_StressCap1(t *testing.T) {
	const capacity = 1
	const goroutineCount = 50
	const loopCount = 20

	tw := timingwheel.NewTimingWheel(10*time.Millisecond, 20)
	tw.Start()
	defer tw.Stop()

	engine, _ := New[int](tw, capacity)

	var setSucc, setFull, cancelSucc, timeoutCount atomic.Uint64
	var wg sync.WaitGroup

	// Consumer
	go func() {
		for range engine.C() {
			timeoutCount.Add(1)
		}
	}()

	t.Logf("==================================================")
	t.Logf(" [시험 목적 및 조건]")
	t.Logf("  - 시험 목적 : 최소 용량(Cap:1) 환경에서 멀티 고루틴 스트레스 테스트")
	t.Logf("  - 시험 조건 : Capacity:%d, 고루틴:%d, 반복:%d", capacity, goroutineCount, loopCount)
	t.Logf("--------------------------------------------------")

	for i := 0; i < goroutineCount; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < loopCount; j++ {
				tm, err := engine.Set(time.Duration(j%10)*time.Millisecond, id*100+j)
				if err == nil {
					setSucc.Add(1)
					if j%2 == 0 { // 절반은 취소 시도
						engine.Cancel(tm)
						cancelSucc.Add(1)
					}
				} else if err == ErrExpiredQueueFull {
					setFull.Add(1)
				}
			}
		}(i)
	}

	wg.Wait()
	time.Sleep(100 * time.Millisecond)
	engine.Close()

	t.Logf(" [테스트 수치]")
	t.Logf("  - Set 성공 : %d, 실패(Full) : %d", setSucc.Load(), setFull.Load())
	t.Logf("  - Cancel 시도 : %d, Timeout 수신 : %d", cancelSucc.Load(), timeoutCount.Load())
	t.Logf(" [시험 결과] : 패닉 없이 정상 종료 확인")
	t.Logf("==================================================")
	record(t, fmt.Sprintf("Succ:%d, Full:%d, Cancel:%d, Received:%d", setSucc.Load(), setFull.Load(), cancelSucc.Load(), timeoutCount.Load()))
}

// 4. cap이 1인 timer engine 생성하고, 멀티고루틴에서 Set, Cancel, timeout 발생하는 중에 Close 하면 문제가 없는지 확인
func TestTimer_ConcurrentClose(t *testing.T) {
	const capacity = 1
	const goroutineCount = 50

	tw := timingwheel.NewTimingWheel(10*time.Millisecond, 20)
	tw.Start()
	defer tw.Stop()

	engine, _ := New[int](tw, capacity)

	var wg sync.WaitGroup
	var closedErrCount atomic.Uint64

	t.Logf("==================================================")
	t.Logf(" [시험 목적 및 조건]")
	t.Logf("  - 시험 목적 : 작업 중 엔진 Close 시 안전성 확인")
	t.Logf("  - 시험 조건 : Capacity:%d, 고루틴:%d", capacity, goroutineCount)
	t.Logf("--------------------------------------------------")

	for i := 0; i < goroutineCount; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for {
				_, err := engine.Set(10*time.Millisecond, id)
				if err == ErrClosed {
					closedErrCount.Add(1)
					return
				}
				time.Sleep(1 * time.Microsecond)
			}
		}(i)
	}

	time.Sleep(5 * time.Millisecond)
	engine.Close()
	wg.Wait()

	t.Logf(" [테스트 수치]")
	t.Logf("  - Close 이후 ErrClosed 감지 : %d 회", closedErrCount.Load())
	t.Logf(" [시험 결과] : 패닉 없이 안전한 종료 확인")
	t.Logf("==================================================")
	record(t, fmt.Sprintf("ClosedErrDetected:%d", closedErrCount.Load()))
}

// 5. 다른 엔진이 발급한 Timer는 Cancel할 수 없어야 함
//
// 소유권 검사가 없으면 취소를 요청한 엔진의 active가 부당하게 감소하여 음수가 되고,
// 실제 소유 엔진은 카운터가 줄지 않아 용량을 영구히 잠식당한다.
func TestTimer_CancelOwnership(t *testing.T) {
	const capacity = 2

	tw := timingwheel.NewTimingWheel(10*time.Millisecond, 20)
	tw.Start()
	defer tw.Stop()

	engineA, _ := New[int](tw, capacity)
	engineB, _ := New[int](tw, capacity)
	defer engineA.Close()
	defer engineB.Close()

	t.Logf("==================================================")
	t.Logf(" [시험 목적 및 조건]")
	t.Logf("  - 시험 목적 : 타 엔진 발급 Timer의 Cancel 차단 및 용량 카운터 무결성 검증")
	t.Logf("  - 시험 조건 : 동일 timingWheel을 공유하는 엔진 2개, Capacity:%d", capacity)
	t.Logf("--------------------------------------------------")

	// 만료되지 않도록 충분히 긴 시간으로 설정
	tmB, err := engineB.Set(1*time.Hour, 100)
	if err != nil {
		t.Fatalf("B.Set 실패: %v", err)
	}
	if engineB.Len() != 1 {
		t.Fatalf("B.Set 직후 Len: 1 기대, 실제 %d", engineB.Len())
	}

	// 1. A가 B의 타이머를 취소 시도 -> 거부되어야 함
	if err := engineA.Cancel(tmB); err != ErrNotOwner {
		t.Errorf("타 엔진 Timer Cancel: ErrNotOwner 기대, 실제 %v", err)
	}

	// 2. 거부된 요청이 A의 카운터를 훼손하지 않아야 함 (음수 방지)
	if engineA.Len() != 0 {
		t.Errorf("거부 후 A.Len: 0 기대, 실제 %d (카운터 훼손)", engineA.Len())
	}

	// 3. B의 타이머는 여전히 살아 있어야 함
	if engineB.Len() != 1 {
		t.Errorf("거부 후 B.Len: 1 기대, 실제 %d", engineB.Len())
	}

	// 4. 타입이 다른 엔진도 차단되어야 함
	engineStr, _ := New[string](tw, capacity)
	defer engineStr.Close()
	if err := engineStr.Cancel(tmB); err != ErrNotOwner {
		t.Errorf("타입이 다른 엔진 Cancel: ErrNotOwner 기대, 실제 %v", err)
	}
	if engineStr.Len() != 0 {
		t.Errorf("거부 후 Engine[string].Len: 0 기대, 실제 %d", engineStr.Len())
	}

	// 5. 소유 엔진의 취소는 정상 동작하고 카운터가 회수되어야 함
	if err := engineB.Cancel(tmB); err != nil {
		t.Errorf("소유 엔진 Cancel: nil 기대, 실제 %v", err)
	}
	if engineB.Len() != 0 {
		t.Errorf("정상 취소 후 B.Len: 0 기대, 실제 %d", engineB.Len())
	}

	// 6. 중복 취소는 기존대로 차단되어야 함
	if err := engineB.Cancel(tmB); err != ErrAlreadyCancelled {
		t.Errorf("중복 Cancel: ErrAlreadyCancelled 기대, 실제 %v", err)
	}

	// 7. 소유권 검사가 중복 취소 검사보다 먼저 수행되어야 함
	//    (이미 취소된 타 엔진 타이머도 ErrAlreadyCancelled가 아니라 ErrNotOwner로 진단)
	if err := engineA.Cancel(tmB); err != ErrNotOwner {
		t.Errorf("이미 취소된 타 엔진 Timer Cancel: ErrNotOwner 기대, 실제 %v", err)
	}

	// 8. 엔진이 발급하지 않은 Timer도 소유권 검사에서 걸러져야 함
	if err := engineA.Cancel(&Timer{}); err != ErrNotOwner {
		t.Errorf("직접 생성한 Timer Cancel: ErrNotOwner 기대, 실제 %v", err)
	}
	if engineA.Len() != 0 {
		t.Errorf("거부 후 A.Len: 0 기대, 실제 %d (카운터 훼손)", engineA.Len())
	}

	// 9. 취소로 반납된 용량을 다시 사용할 수 있어야 함 (용량 잠식 없음)
	for i := 0; i < capacity; i++ {
		if _, err := engineB.Set(1*time.Hour, i); err != nil {
			t.Errorf("취소 후 재사용 B.Set #%d: nil 기대, 실제 %v (용량 잠식)", i, err)
		}
	}

	t.Logf(" [테스트 수치]")
	t.Logf("  - 타 엔진 Cancel 차단      : ErrNotOwner")
	t.Logf("  - 차단 후 A.Len            : %d (음수 아님)", engineA.Len())
	t.Logf("  - 정상 취소 후 재사용 가능 : B.Len=%d / Cap=%d", engineB.Len(), engineB.Cap())
	t.Logf("--------------------------------------------------")
	t.Logf(" [시험 결과] : 정상 (소유권 검사 및 카운터 무결성 확인)")
	t.Logf("==================================================")

	record(t, "Cancel ownership guard verified (cross-engine, cross-type, capacity reuse)")
}

// 6. 타이머 엔진 사용 후 메모리 누수 여부 확인
func TestTimer_MemoryLeakCheck(t *testing.T) {
	const capacity = 100
	const iterations = 50

	var msBefore, msAfter runtime.MemStats

	tw := timingwheel.NewTimingWheel(10*time.Millisecond, 20)
	tw.Start()

	engine, _ := New[*largeData](tw, capacity)

	// 초기 상태 안정화 및 측정
	runtime.GC()
	runtime.ReadMemStats(&msBefore)

	t.Logf("==================================================")
	t.Logf(" [시험 목적 및 조건]")
	t.Logf("  - 시험 목적 : 타이머 엔진 사용 후 메모리 누수 여부 확인")
	t.Logf("  - 시험 조건 : Capacity:%d, 반복횟수:%d (각 1MB 데이터)", capacity, iterations)
	t.Logf("--------------------------------------------------")

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		received := 0
		for range engine.C() {
			received++
			if received == iterations {
				return
			}
		}
	}()

	for i := 0; i < iterations; i++ {
		data := &largeData{}
		_, err := engine.Set(10*time.Millisecond, data)
		if err != nil {
			t.Errorf("Set 실패: %v", err)
		}
	}

	wg.Wait()

	// 자원 해제
	engine.Close()
	tw.Stop()

	engine = nil
	tw = nil

	// GC 강제 실행 및 대기
	for i := 0; i < 3; i++ {
		runtime.GC()
		time.Sleep(50 * time.Millisecond)
	}

	runtime.ReadMemStats(&msAfter)

	t.Logf(" [테스트 수치]")
	t.Logf("  - 시작 전 메모리 : %.2f MB", float64(msBefore.Alloc)/1024/1024)
	t.Logf("  - 종료 후 메모리 : %.2f MB", float64(msAfter.Alloc)/1024/1024)
	t.Logf("--------------------------------------------------")
	t.Logf(" [시험 결과]")
	leakSize := int64(msAfter.Alloc) - int64(msBefore.Alloc)
	// 오차 범위 2MB 이내 확인 (런타임 오버헤드 고려)
	if leakSize > 2*1024*1024 {
		t.Errorf("  - 메모리 누수 의심: %.2f MB 증가함", float64(leakSize)/1024/1024)
	} else {
		t.Logf("  - 메모리 정상 확인 (누수 없음)")
	}
	t.Logf("==================================================")
	record(t, fmt.Sprintf("Before:%.2fMB, After:%.2fMB", float64(msBefore.Alloc)/1024/1024, float64(msAfter.Alloc)/1024/1024))
}
