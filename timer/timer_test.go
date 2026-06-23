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
			} else if err == ErrExpiredQFull {
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
				} else if err == ErrExpiredQFull {
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

// 5. 타이머 엔진 사용 후 메모리 누수 여부 확인
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
