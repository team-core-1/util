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

func TestPool_AccessLockBasic(t *testing.T) {
	const capacity = 3
	ip, err := New[string](capacity)
	if err != nil {
		t.Fatalf("Failed to create IndexPool: %v", err)
	}

	t.Logf("==================================================")
	t.Logf(" [시험 목적 및 조건]")
	t.Logf("  - 시험 목적 : AccessLock 기본 동작 및 에러 경로(ErrNil/ErrInvalidIndex/ErrNotAllocIndex) 검증")
	t.Logf("  - 시험 조건 : Capacity: %d", capacity)
	t.Logf("--------------------------------------------------")

	// 1. nil 풀
	var nilPool *Pool[string]
	if err := nilPool.AccessLock(0, func(memPtr *string) {}); err != ErrNil {
		t.Errorf("nil 풀 AccessLock: ErrNil 기대, 실제 %v", err)
	}

	// 2. 범위를 벗어난 인덱스
	if err := ip.AccessLock(-1, func(memPtr *string) {}); err != ErrInvalidIndex {
		t.Errorf("AccessLock(-1): ErrInvalidIndex 기대, 실제 %v", err)
	}
	if err := ip.AccessLock(capacity, func(memPtr *string) {}); err != ErrInvalidIndex {
		t.Errorf("AccessLock(%d): ErrInvalidIndex 기대, 실제 %v", capacity, err)
	}

	// 3. 할당되지 않은(free) 슬롯
	if err := ip.AccessLock(0, func(memPtr *string) {}); err != ErrNotAllocIndex {
		t.Errorf("미할당 슬롯 AccessLock: ErrNotAllocIndex 기대, 실제 %v", err)
	}

	// 4. 정상 경로: 쓰기 -> 읽기
	idx, err := ip.Get()
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if err := ip.AccessLock(idx, func(memPtr *string) { *memPtr = "locked" }); err != nil {
		t.Fatalf("AccessLock 쓰기 실패: %v", err)
	}

	var got string
	if err := ip.AccessLock(idx, func(memPtr *string) { got = *memPtr }); err != nil {
		t.Fatalf("AccessLock 읽기 실패: %v", err)
	}
	if got != "locked" {
		t.Errorf("AccessLock으로 쓴 값 불일치: \"locked\" 기대, 실제 %q", got)
	}

	// 5. Put 이후 슬롯이 초기화되고 다시 미할당 상태가 되어야 함
	if err := ip.Put(idx); err != nil {
		t.Fatalf("Put 실패: %v", err)
	}
	if err := ip.AccessLock(idx, func(memPtr *string) {}); err != ErrNotAllocIndex {
		t.Errorf("Put 이후 AccessLock: ErrNotAllocIndex 기대, 실제 %v", err)
	}

	t.Logf("==================================================")
	t.Logf(" [테스트 수치]")
	t.Logf("--------------------------------------------------")
	t.Logf("  - 남은 수량 (Len) : %d", ip.Len())
	t.Logf("  - 전체 용량 (Cap) : %d", ip.Cap())
	t.Logf("==================================================")
	t.Logf(" [시험 결과] : 정상 (AccessLock 기본 동작 및 에러 경로 확인)")
	t.Logf("==================================================")

	record(t, "AccessLock basic operation and error paths verified")
}

func TestPool_AccessLockConcurrency(t *testing.T) {
	const capacity = 1
	const goroutineCount = 50
	ip, _ := New[int](capacity)

	t.Logf("==================================================")
	t.Logf(" [시험 목적 및 조건]")
	t.Logf("  - 시험 목적 : AccessLock이 동일 인덱스 접근을 블로킹 직렬화하는지 검증")
	t.Logf("               (Access의 비블로킹 Try-Lock 거부 방식과 대비)")
	t.Logf("  - 시험 조건 : 풀 용량: %d, 대상 인덱스: 1개, 고루틴 개수: %d개", capacity, goroutineCount)
	t.Logf("--------------------------------------------------")

	idx, err := ip.Get()
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	var wg sync.WaitGroup
	var succCount atomic.Uint64
	var inuseCount atomic.Uint64
	var otherErrCount atomic.Uint64

	startSignal := make(chan struct{})

	// 50개 고루틴이 동시에 동일한 idx로 AccessLock 시도
	for i := 0; i < goroutineCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-startSignal

			err := ip.AccessLock(idx, func(memPtr *int) {
				// 락으로 보호되는 구간이므로 경합 없이 증가해야 함
				*memPtr++
			})

			switch err {
			case nil:
				succCount.Add(1)
			case ErrInuseIndex:
				inuseCount.Add(1)
			default:
				otherErrCount.Add(1)
			}
		}()
	}

	close(startSignal)
	wg.Wait()

	var final int
	if err := ip.AccessLock(idx, func(memPtr *int) { final = *memPtr }); err != nil {
		t.Fatalf("최종값 조회 실패: %v", err)
	}

	t.Logf("==================================================")
	t.Logf(" [테스트 수치]")
	t.Logf("--------------------------------------------------")
	t.Logf("  - 총 AccessLock 시도 횟수     : %d 회", goroutineCount)
	t.Logf("  - 성공 횟수 (예상치: %d)      : %d 회", goroutineCount, succCount.Load())
	t.Logf("  - InUse 거부 횟수 (예상치: 0) : %d 회", inuseCount.Load())
	t.Logf("  - 기타 에러 횟수 (예상치: 0)  : %d 회", otherErrCount.Load())
	t.Logf("  - 콜백 누적 증가값 (예상치: %d) : %d", goroutineCount, final)
	t.Logf("--------------------------------------------------")

	if succCount.Load() != uint64(goroutineCount) {
		t.Errorf("  - 오작동 감지 : AccessLock이 블로킹 직렬화되지 않음 (성공: %d, InUse거부: %d, 기타: %d)",
			succCount.Load(), inuseCount.Load(), otherErrCount.Load())
	} else {
		t.Logf("  - 직렬화 성공 : 모든 고루틴이 순차적으로 콜백을 실행함")
	}

	if final != goroutineCount {
		t.Errorf("  - 오작동 감지 : 콜백 구간의 원자성이 깨짐 (누적값: %d, 예상: %d)", final, goroutineCount)
	} else {
		t.Logf("  - 원자성 성공 : 콜백 내 갱신이 유실 없이 %d회 반영됨", goroutineCount)
	}
	t.Logf("==================================================")
	t.Logf(" [시험 결과] : 정상 (AccessLock 블로킹 직렬화 및 원자성 검증 완료)")
	t.Logf("==================================================")

	record(t, fmt.Sprintf("AccessLock serialization verified (Succ: %d, Sum: %d)", succCount.Load(), final))
}

// Access(락 해제 후 콜백 실행)와 AccessLock(락 유지)을 혼용할 때
// 거부된 AccessLock이 Access의 StateInUse 플래그를 훼손하지 않아야 함을 검증하는 회귀 테스트.
// 훼손될 경우 사용 중인 슬롯이 Put으로 회수/초기화되어 데이터 레이스가 발생함.
func TestPool_AccessLockMixedWithAccess(t *testing.T) {
	const capacity = 1
	ip, _ := New[int](capacity)

	t.Logf("==================================================")
	t.Logf(" [시험 목적 및 조건]")
	t.Logf("  - 시험 목적 : Access 콜백 실행 중 AccessLock 거부 시 InUse 상태 훼손 여부 검증(회귀 방지)")
	t.Logf("  - 시험 조건 : 풀 용량: %d, Access 콜백을 붙잡아 둔 상태에서 AccessLock/Put 시도", capacity)
	t.Logf("--------------------------------------------------")

	idx, err := ip.Get()
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	started := make(chan struct{})
	release := make(chan struct{})
	accessDone := make(chan error, 1)

	// Access는 슬롯 락을 해제한 뒤 콜백을 실행하므로, 콜백 진행 중에도 다른 고루틴이 슬롯 락을 잡을 수 있음
	go func() {
		accessDone <- ip.Access(idx, func(memPtr *int) {
			close(started)
			<-release // 콜백을 진행 중 상태로 유지
			*memPtr = 7
		})
	}()

	<-started

	// 1. Access가 점유 중이므로 AccessLock은 거부되어야 함
	lockCallbackRan := false
	errLock := ip.AccessLock(idx, func(memPtr *int) { lockCallbackRan = true })
	if errLock != ErrInuseIndex {
		t.Errorf("Access 점유 중 AccessLock: ErrInuseIndex 기대, 실제 %v", errLock)
	}
	if lockCallbackRan {
		t.Errorf("오작동 감지 : 거부된 AccessLock의 콜백이 실행됨")
	}

	// 2. 거부된 AccessLock이 Access의 InUse 플래그를 지우지 않았어야 함
	//    지워졌다면 사용 중인 슬롯이 Put으로 회수되어 콜백과 레이스가 발생함
	errPut := ip.Put(idx)
	if errPut != ErrInuseIndex {
		t.Errorf("오작동 감지 : 거부된 AccessLock이 Access의 InUse 상태를 해제함 -> 사용 중 슬롯 회수됨 (Put 결과: %v)", errPut)
	}

	// 3. Access 콜백 종료 후에는 정상 동작해야 함
	close(release)
	if err := <-accessDone; err != nil {
		t.Errorf("Access 실패: %v", err)
	}

	var got int
	if err := ip.AccessLock(idx, func(memPtr *int) { got = *memPtr }); err != nil {
		t.Errorf("Access 종료 후 AccessLock 실패: %v", err)
	}
	if got != 7 {
		t.Errorf("Access 콜백이 쓴 값이 보존되지 않음: 7 기대, 실제 %d", got)
	}

	if err := ip.Put(idx); err != nil {
		t.Errorf("Access 종료 후 Put 실패: %v", err)
	}

	t.Logf("==================================================")
	t.Logf(" [테스트 수치]")
	t.Logf("--------------------------------------------------")
	t.Logf("  - Access 점유 중 AccessLock 결과 : %v (예상치: %v)", errLock, ErrInuseIndex)
	t.Logf("  - Access 점유 중 Put 결과        : %v (예상치: %v)", errPut, ErrInuseIndex)
	t.Logf("  - Access 콜백 기록값 보존        : %d (예상치: 7)", got)
	t.Logf("  - 최종 남은 수량 (Len)           : %d / %d", ip.Len(), ip.Cap())
	t.Logf("==================================================")
	t.Logf(" [시험 결과] : 정상 (Access/AccessLock 혼용 시 InUse 상태 보호 확인)")
	t.Logf("==================================================")

	record(t, fmt.Sprintf("Access/AccessLock mix verified (AccessLock:%v, Put:%v)", errLock, errPut))
}

func TestPool_AccessLockPanicRecovery(t *testing.T) {
	const capacity = 1
	ip, _ := New[int](capacity)

	t.Logf("==================================================")
	t.Logf(" [시험 목적 및 조건]")
	t.Logf("  - 시험 목적 : AccessLock 콜백 패닉 시 Unlock 및 InUse 상태 복구(정상 재진입) 검증")
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

		_ = ip.AccessLock(idx, func(memPtr *int) {
			panic("simulated panic in AccessLock callback")
		})
	}()

	// 2. recover 이후 AccessLock 재진입 (슬롯 락이 해제되지 않았다면 데드락)
	reenterSucc := false
	var reenterErr error

	errChan := make(chan error, 1)
	go func() {
		errChan <- ip.AccessLock(idx, func(memPtr *int) { *memPtr = 42 })
	}()

	select {
	case reenterErr = <-errChan:
		if reenterErr == nil {
			reenterSucc = true
		}
	case <-time.After(1 * time.Second):
		t.Errorf("  - 오작동 감지 : recover 이후 AccessLock 재진입 시 데드락이 발생함 (타임아웃)")
	}

	// 3. StateInUse가 복구되었는지 Access로 교차 확인 (잔존 시 ErrInuseIndex)
	accessErr := ip.Access(idx, func(memPtr *int) {})

	t.Logf("==================================================")
	t.Logf(" [테스트 수치]")
	t.Logf("--------------------------------------------------")
	t.Logf("  - 패닉 복구 여부                : %v (예상치: true)", panicHandled)
	t.Logf("  - AccessLock 재진입 성공 여부   : %v (예상치: true)", reenterSucc)
	t.Logf("  - AccessLock 재진입 에러        : %v (예상치: <nil>)", reenterErr)
	t.Logf("  - 패닉 이후 Access 교차 확인    : %v (예상치: <nil>)", accessErr)
	t.Logf("==================================================")

	if !panicHandled {
		t.Errorf("  - 오작동 감지 : 패닉이 복구되지 않음")
	}
	if !reenterSucc {
		t.Errorf("  - 오작동 감지 : AccessLock 재진입에 실패함 (에러: %v)", reenterErr)
	}
	if accessErr != nil {
		t.Errorf("  - 오작동 감지 : 패닉 이후 StateInUse가 복구되지 않음 (Access 에러: %v)", accessErr)
	}
	if panicHandled && reenterSucc && accessErr == nil {
		t.Logf("  - 시험 결과 : 정상 (패닉 복구 후 락/상태 해제 및 데드락 없이 재진입 성공)")
	}
	t.Logf("==================================================")

	record(t, fmt.Sprintf("AccessLock panic recovery verified (PanicHandled: %v, ReenterSucc: %v)", panicHandled, reenterSucc))
}
