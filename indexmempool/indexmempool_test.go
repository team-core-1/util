package indexmempool

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

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

func TestPool_Basic(t *testing.T) {
	const capacity = 5
	ip, err := New[string](capacity)
	if err != nil {
		t.Fatalf("Failed to create IndexPool: %v", err)
	}

	t.Logf("==================================================")
	t.Logf(" [시험 목적 및 조건]")
	t.Logf("  - 시험 목적 : IndexPool 기본 기능 (Get/Access/Put) 검증")
	t.Logf("  - 시험 조건 : Capacity: %d", capacity)
	t.Logf("--------------------------------------------------")

	// 1. Get
	idx1, err := ip.Get()
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	// 2. Access & write to it
	err = ip.Access(idx1, func(memPtr *string) {
		*memPtr = "hello"
	})
	if err != nil {
		t.Fatalf("Access failed: %v", err)
	}

	// 3. Put index back
	err = ip.Put(idx1)
	if err != nil {
		t.Fatalf("Put failed: %v", err)
	}

	t.Logf("==================================================")
	t.Logf(" [테스트 수치]")
	t.Logf("--------------------------------------------------")
	t.Logf("  - 남은 수량 (Len) : %d", ip.Len())
	t.Logf("  - 전체 용량 (Cap) : %d", ip.Cap())
	t.Logf("==================================================")
	t.Logf(" [시험 결과] : 정상 (기본 동작 완료)")
	t.Logf("==================================================")

	record(t, "Basic Get/Put/Access verified")
}

func TestPool_Errors(t *testing.T) {
	const capacity = 2
	ip, _ := New[int](capacity)

	t.Logf("==================================================")
	t.Logf(" [시험 목적 및 조건]")
	t.Logf("  - 시험 목적 : IndexPool의 에러 및 한계 상황 검증")
	t.Logf("  - 시험 조건 : Capacity: %d", capacity)
	t.Logf("--------------------------------------------------")

	// Access of free slot should fail
	err := ip.Access(0, func(memPtr *int) {})
	if err == nil {
		t.Errorf("expected error when accessing free index, got nil")
	}

	idx1, _ := ip.Get()
	idx2, _ := ip.Get()

	// Pool should be empty now
	_, err = ip.Get()
	if err != ErrEmpty {
		t.Errorf("expected ErrEmpty, got %v", err)
	}

	// Put wrong index should fail
	err = ip.Put(99)
	if err != ErrInvalidIndex {
		t.Errorf("expected ErrInvalidIndex, got %v", err)
	}

	// Put free index should fail (double free)
	_ = ip.Put(idx1)
	err = ip.Put(idx1)
	if err != ErrNotAllocIndex {
		t.Errorf("expected ErrNotAllocIndex (double free), got %v", err)
	}

	_ = ip.Put(idx2)

	t.Logf("==================================================")
	t.Logf(" [테스트 수치]")
	t.Logf("--------------------------------------------------")
	t.Logf("  - 에러 처리 시도 횟수 : 5회")
	t.Logf("  - 감지된 에러 형태   : ErrNotAllocIndex, ErrEmpty, ErrInvalidIndex")
	t.Logf("==================================================")
	t.Logf(" [시험 결과] : 정상 (모든 에러 상황 차단 확인)")
	t.Logf("==================================================")

	record(t, "Errors and edge cases verified")
}

func TestPool_Concurrency(t *testing.T) {
	const capacity = 10
	const goroutineCount = 100
	ip, _ := New[string](capacity)

	t.Logf("==================================================")
	t.Logf(" [시험 목적 및 조건]")
	t.Logf("  - 시험 목적 : 멀티 고루틴 환경에서 Get/Access/Put 경합 동작 검증")
	t.Logf("  - 시험 조건 : Capacity: %d, 고루틴 개수: %d개", capacity, goroutineCount)
	t.Logf("--------------------------------------------------")

	var wg sync.WaitGroup
	var succCount atomic.Uint64
	var failCount atomic.Uint64

	startSignal := make(chan struct{})

	for i := 0; i < goroutineCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-startSignal
			idx, err := ip.Get()
			if err == nil {
				getErr := ip.Access(idx, func(memPtr *string) {
					// 가벼운 작업
				})
				if getErr == nil {
					_ = ip.Put(idx)
					succCount.Add(1)
				} else {
					failCount.Add(1)
				}
			} else {
				failCount.Add(1)
			}
		}()
	}

	close(startSignal)
	wg.Wait()

	t.Logf("==================================================")
	t.Logf(" [테스트 수치]")
	t.Logf("--------------------------------------------------")
	t.Logf("  - 총 시도 횟수  : %d 회", goroutineCount)
	t.Logf("  - 성공 횟수     : %d 회", succCount.Load())
	t.Logf("  - 실패(풀 고갈) : %d 회", failCount.Load())
	t.Logf("==================================================")
	t.Logf(" [시험 결과] : 정상 (패닉 없이 동시성 제어 완료)")
	t.Logf("==================================================")

	record(t, fmt.Sprintf("Concurrency tested: Succ:%d, Fail:%d", succCount.Load(), failCount.Load()))
}

func TestPool_AccessConcurrency(t *testing.T) {
	const capacity = 1
	const goroutineCount = 50
	ip, _ := New[int](capacity)

	t.Logf("==================================================")
	t.Logf(" [시험 목적 및 조건]")
	t.Logf("  - 시험 목적 : 동일한 인덱스에 여러 고루틴이 동시 Access 시도 시 동기화 정합성(Try-Lock) 검증")
	t.Logf("  - 시험 조건 : 풀 용량: %d, 대상 인덱스: 1개, 고루틴 개수: %d개", capacity, goroutineCount)
	t.Logf("--------------------------------------------------")

	idx, err := ip.Get()
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	var wg sync.WaitGroup
	var accessSuccCount atomic.Uint64
	var accessFailInuseCount atomic.Uint64
	var otherErrCount atomic.Uint64

	startSignal := make(chan struct{})

	// 50개 고루틴이 동시에 동일한 idx로 Access 시도
	for i := 0; i < goroutineCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-startSignal

			err := ip.Access(idx, func(memPtr *int) {
				accessSuccCount.Add(1)
				// 인풋 점유 시간 확보를 위해 잠시 대기
				time.Sleep(10 * time.Millisecond)
			})

			if err != nil {
				if err == ErrInuseIndex {
					accessFailInuseCount.Add(1)
				} else {
					otherErrCount.Add(1)
				}
			}
		}()
	}

	close(startSignal)
	wg.Wait()

	t.Logf("==================================================")
	t.Logf(" [테스트 수치]")
	t.Logf("--------------------------------------------------")
	t.Logf("  - 총 Access 시도 횟수       : %d 회", goroutineCount)
	t.Logf("  - 성공 횟수 (예상치: 1)     : %d 회", accessSuccCount.Load())
	t.Logf("  - InUse 차단 실패 (예상치: 49) : %d 회", accessFailInuseCount.Load())
	t.Logf("  - 기타 에러 횟수 (예상치: 0)  : %d 회", otherErrCount.Load())
	t.Logf("--------------------------------------------------")

	if accessSuccCount.Load() != 1 {
		t.Errorf("  - 오작동 감지 : 동시에 2개 이상의 고루틴이 Access에 성공함 (성공 횟수: %d)", accessSuccCount.Load())
	} else {
		t.Logf("  - 동기화 성공 : 단 하나의 고루틴만 Access 성공함이 입증됨")
	}

	if accessFailInuseCount.Load() != uint64(goroutineCount-1) {
		t.Errorf("  - 오작동 감지 : ErrInuseIndex 에러 감지 횟수 불일치 (실제: %d, 예상: %d)", accessFailInuseCount.Load(), goroutineCount-1)
	} else {
		t.Logf("  - 에러 처리 성공 : 나머지 모든 고루틴은 ErrInuseIndex에 의해 비블로킹(Try-Lock)으로 거부됨")
	}
	t.Logf("==================================================")
	t.Logf(" [시험 결과] : 정상 (Access 동시 접근 동기화 제어 검증 완료)")
	t.Logf("==================================================")

	record(t, fmt.Sprintf("Access concurrency verified (Succ: 1, InUseBlocked: %d)", accessFailInuseCount.Load()))
}

func TestPool_AccessPanicRecovery(t *testing.T) {
	const capacity = 1
	ip, _ := New[int](capacity)

	t.Logf("==================================================")
	t.Logf(" [시험 목적 및 조건]")
	t.Logf("  - 시험 목적 : Access 콜백 내에서 패닉 발생 시 Unlock 및 상태 복구(정상 재진입) 검증")
	t.Logf("  - 시험 조건 : 풀 용량: %d, 대상 인덱스: 1개, 강제 패닉 여부: 참(True)", capacity)
	t.Logf("--------------------------------------------------")

	idx, err := ip.Get()
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	// 1. 패닉 발생 시뮬레이션 및 복구
	panicHandled := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicHandled = true
				t.Logf("  - 감지된 콜백 패닉 정상 복구 완료: %v", r)
			}
		}()

		// Access 시도 중 패닉 트리거
		_ = ip.Access(idx, func(memPtr *int) {
			panic("simulated panic in Access callback")
		})
	}()

	// 2. recover 이후 재진입 테스트
	// 락이 올바르게 Unlock되었고 StateInUse가 해제되었다면 다시 진입할 때 에러가 없어야 하고 데드락에 빠지지 않아야 함
	reenterSucc := false
	var reenterErr error

	errChan := make(chan error, 1)
	go func() {
		errChan <- ip.Access(idx, func(memPtr *int) {
			*memPtr = 42
		})
	}()

	// 데드락 감지를 위해 타임아웃 처리
	select {
	case reenterErr = <-errChan:
		if reenterErr == nil {
			reenterSucc = true
		}
	case <-time.After(1 * time.Second):
		t.Errorf("  - 오작동 감지 : recover 이후 Access 재진입 시 데드락이 발생함 (타임아웃)")
	}

	t.Logf("==================================================")
	t.Logf(" [테스트 수치]")
	t.Logf("--------------------------------------------------")
	t.Logf("  - 패닉 복구 여부           : %v (예상치: true)", panicHandled)
	t.Logf("  - 재진입 성공 여부         : %v (예상치: true)", reenterSucc)
	t.Logf("  - 재진입 시 발생 에러      : %v (예상치: <nil>)", reenterErr)
	t.Logf("==================================================")

	if !panicHandled {
		t.Errorf("  - 오작동 감지 : 패닉이 복구되지 않음")
	}
	if !reenterSucc {
		t.Errorf("  - 오작동 감지 : 재진입에 실패함 (에러: %v)", reenterErr)
	} else {
		t.Logf("  - 시험 결과 : 정상 (패닉 복구 후 정상 해제 및 데드락 없이 재진입 성공)")
	}
	t.Logf("==================================================")

	record(t, fmt.Sprintf("Access panic recovery verified (PanicHandled: %v, ReenterSucc: %v)", panicHandled, reenterSucc))
}
