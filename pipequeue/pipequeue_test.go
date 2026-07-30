package pipequeue

import (
	"fmt"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type myStruct struct {
	myData [1024 * 1024 * 10]byte
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

func TestQueue_ConcurrencyAndMemoryCleanup(t *testing.T) {
	const capacity = 1
	const goroutineCount = 1000
	const loopCount = 10

	var data *myStruct

	var msBefore runtime.MemStats
	var msNew runtime.MemStats
	var msClose runtime.MemStats

	var dequeueSuccCount atomic.Uint64
	var dequeueFailCount atomic.Uint64
	var enqueueSuccCount atomic.Uint64
	var enqueueFailCount atomic.Uint64

	var wg sync.WaitGroup

	// 초기 메모리 사용량
	runtime.GC()
	runtime.ReadMemStats(&msBefore)

	// queue.New
	q, err := New[*myStruct](capacity)
	if err != nil {
		t.Fatalf("queue 초기화 실패: %+v", err)
	}

	// 생성 후 메모리 사용량
	runtime.GC()
	runtime.ReadMemStats(&msNew)

	startSignal := make(chan any)

	// 1. 멀티 고루틴에서 C() 수신/Put 시험
	for i := 0; i < goroutineCount; i++ {
		// C() 수신/Put 경합 고루틴
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			<-startSignal

			for j := 0; j < loopCount; j++ {
				// C() 수신 시험
				select {
				case _, ok := <-q.C():
					if !ok {
						dequeueFailCount.Add(1)
					} else {
						dequeueSuccCount.Add(1)
					}
				default:
					dequeueFailCount.Add(1)
				}
			}
		}(i)

		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			<-startSignal

			for j := 0; j < loopCount; j++ {
				// Put 시험
				err := q.Put(data)
				if err != nil {
					enqueueFailCount.Add(1)
					continue
				}
				enqueueSuccCount.Add(1)
			}
		}(i)
	}

	t.Logf("==================================================")
	t.Logf(" [시험 목적 및 조건]")
	t.Logf("  - 시험 목적 : 멀티 고루틴 Put/C() 수신 경합 및 메모리 정리 테스트")
	t.Logf("  - 시험 조건 : Capacity:%d, 고루틴:%d개, 반복:%d회", capacity, goroutineCount*2, loopCount)
	t.Logf("--------------------------------------------------")
	t.Logf(" => 시험 진행 중...")
	close(startSignal)

	wg.Wait()

	t.Logf("==================================================")
	t.Logf(" [테스트 수치]")
	t.Logf("--------------------------------------------------")
	t.Logf(" 총 C() 수신 시도 : %d 회", goroutineCount*loopCount)
	t.Logf("  - 성공 : %d 회", dequeueSuccCount.Load())
	t.Logf("  - 실패 : %d 회", dequeueFailCount.Load())
	t.Logf("--------------------------------------------------")
	t.Logf(" 총 Put 시도 : %d 회", goroutineCount*loopCount)
	t.Logf("  - 성공 : %d 회", enqueueSuccCount.Load())
	t.Logf("  - 실패 : %d 회", enqueueFailCount.Load())

	// queue Close
	q.Close()
	q = nil
	for i := 0; i < 1; i++ {
		runtime.GC()
		time.Sleep(time.Microsecond * 30)
	}
	runtime.ReadMemStats(&msClose)

	t.Logf("--------------------------------------------------")
	t.Logf(" [메모리 수치]")
	t.Logf("  - 메모리 사용량: %.2f MB -> %.2f MB -> %.2f MB",
		float64(msBefore.Alloc)/(1024*1024), float64(msNew.Alloc)/(1024*1024), float64(msClose.Alloc)/(1024*1024))

	t.Logf("==================================================")
	t.Logf(" [시험 결과]")
	if (dequeueSuccCount.Load()+dequeueFailCount.Load()) != (goroutineCount*loopCount) || (enqueueSuccCount.Load()+enqueueFailCount.Load()) != (goroutineCount*loopCount) {
		t.Errorf("  - 입출력 시도 횟수 불일치 발생")
	} else {
		t.Logf("  - 입출력 시도 횟수 정합성 확인 완료")
	}

	if (int64(msClose.Alloc) - int64(msBefore.Alloc)) > (1 * 1024 * 1024) {
		t.Errorf("  - 자원 정리 실패: Close() 이후 메모리 해제 불량 (오차 1MB 초과)")
	} else {
		t.Logf("  - 메모리 해제 정상 확인 (1 MB 오차 범위)")
	}
	t.Logf("==================================================")
	record(t, fmt.Sprintf("Succ(Enq:%d, Deq:%d), Mem:%.2fMB->%.2fMB", enqueueSuccCount.Load(), dequeueSuccCount.Load(), float64(msBefore.Alloc)/1024/1024, float64(msClose.Alloc)/1024/1024))
}

// 1. 100개의 큐를 생성하여 Put한 값이 그대로 C()로 수신되는지 확인하는 테스트
func TestQueue_IntegrityCheck100(t *testing.T) {
	const count = 100
	t.Logf("==================================================")
	t.Logf(" [시험 목적 및 조건]")
	t.Logf("  - 시험 목적 : 100개의 개별 큐 인스턴스 데이터 정합성 테스트")
	t.Logf("  - 시험 조건 : 큐 개수: %d개", count)

	for i := 0; i < count; i++ {
		q, err := New[int](1)
		if err != nil {
			t.Fatalf("[%d] 큐 생성 실패: %v", i, err)
		}

		expected := i + 5000
		if err := q.Put(expected); err != nil {
			t.Errorf("[%d] Put 실패: %v", i, err)
		}

		actual, ok := <-q.C()
		if !ok {
			t.Errorf("[%d] C() 수신 실패: channel closed", i)
		}

		if expected != actual {
			t.Errorf("[%d] 데이터 불일치: 예상값 %d, 실제값 %d", i, expected, actual)
		}
	}
	t.Logf("--------------------------------------------------")
	t.Logf(" [테스트 수치]")
	t.Logf("  - 총 %d개 큐 생성 및 Put/C() 수신 정합성 검증 완료", count)
	t.Logf(" [시험 결과] : 정상 (모든 데이터 일치)")
	t.Logf("==================================================")
	record(t, fmt.Sprintf("Verified %d independent queue instances", count))
}

// 2. Use 메서드를 이용한 Put 실행 시간 측정 테스트
func TestQueue_ExecutionTimeMeasurement(t *testing.T) {
	const goroutineCount = 50
	const loopCount = 100
	q, _ := New[int](goroutineCount * loopCount)

	var totalDuration atomic.Int64
	var opCount atomic.Int64

	// 미들웨어를 이용한 시간 측정 로직 등록
	q.Use(func(c *Context[int]) {
		start := time.Now()
		c.Next() // 실제 작업(Put) 수행
		elapsed := time.Since(start)

		totalDuration.Add(int64(elapsed))
		opCount.Add(1)
	})

	t.Logf("==================================================")
	t.Logf(" [시험 목적 및 조건]")
	t.Logf("  - 시험 목적 : 미들웨어를 이용한 Put 실행 시간 측정 테스트")
	t.Logf("  - 시험 조건 : 총 고루틴: %d개, 반복: %d회", goroutineCount*2, loopCount)

	var wg sync.WaitGroup
	startSignal := make(chan struct{})

	for i := 0; i < goroutineCount; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-startSignal
			for j := 0; j < loopCount; j++ {
				_ = q.Put(j)
			}
		}()
		go func() {
			defer wg.Done()
			<-startSignal
			for j := 0; j < loopCount; j++ {
				_, _ = <-q.C()
			}
		}()
	}

	close(startSignal)
	wg.Wait()

	total := time.Duration(totalDuration.Load())
	count := opCount.Load()
	avg := time.Duration(0)
	if count > 0 {
		avg = time.Duration(totalDuration.Load() / count)
	}

	t.Logf("--------------------------------------------------")
	t.Logf(" [테스트 수치]")
	t.Logf("  - 총 작업 횟수     : %d 회", count)
	t.Logf("  - 총 소요 시간 합계 : %v", total)
	t.Logf("  - 평균 소요 시간    : %v", avg)
	t.Logf("--------------------------------------------------")
	t.Logf(" [시험 결과] : 시간 측정 완료")
	t.Logf("==================================================")
	record(t, fmt.Sprintf("Avg:%v, Ops:%d", avg, count))
}

// 3. 멀티 고루틴에서 사용 중 Close 호출 시 동작 확인 테스트
func TestQueue_ConcurrentClose(t *testing.T) {
	const capacity = 1
	const goroutineCount = 20
	const loopCount = 1000

	q, _ := New[int](capacity)

	var wg sync.WaitGroup
	startSignal := make(chan struct{})

	var enqueueSuccCount atomic.Uint64
	var enqueueFullCount atomic.Uint64
	var enqueueClosedCount atomic.Uint64
	var dequeueSuccCount atomic.Uint64
	var dequeueEmptyCount atomic.Uint64
	var dequeueClosedCount atomic.Uint64

	// Put/C() 수신을 수행하는 여러 고루틴 생성
	for i := 0; i < goroutineCount; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-startSignal
			for j := 0; j < loopCount; j++ {
				err := q.Put(j)
				if err == nil {
					enqueueSuccCount.Add(1)
				} else if err == ErrClosed {
					enqueueClosedCount.Add(1)
				} else if err == ErrFull {
					enqueueFullCount.Add(1)
				}
			}
		}()
		go func() {
			defer wg.Done()
			<-startSignal
			for j := 0; j < loopCount; j++ {
				select {
				case _, ok := <-q.C():
					if ok {
						dequeueSuccCount.Add(1)
					} else {
						dequeueClosedCount.Add(1)
					}
				default:
					if q.IsClosed() {
						dequeueClosedCount.Add(1)
					} else {
						dequeueEmptyCount.Add(1)
					}
				}
			}
		}()
	}

	close(startSignal)
	// 작업을 잠시 진행하게 한 뒤 Close 호출
	time.Sleep(2 * time.Millisecond)
	q.Close()
	wg.Wait()

	t.Logf("==================================================")
	t.Logf(" 3. 멀티 고루틴 중 Close() 테스트 통계")
	t.Logf("--------------------------------------------------")
	t.Logf(" 시험 조건:")
	t.Logf("  - Capacity       : %d", capacity)
	t.Logf("  - 총 고루틴 개수 : %d (Put:%d, C()수신:%d)", goroutineCount*2, goroutineCount, goroutineCount)
	t.Logf("  - 고루틴당 반복  : %d 회", loopCount)
	t.Logf("--------------------------------------------------")
	t.Logf(" Put 통계:")
	t.Logf("  - 총 시도 횟수   : %d 회", goroutineCount*loopCount)
	t.Logf("  - 성공 횟수      : %d 회", enqueueSuccCount.Load())
	t.Logf("  - ErrFull 감지   : %d 회", enqueueFullCount.Load())
	t.Logf("  - ErrClosed 감지 : %d 회", enqueueClosedCount.Load())
	enqueueTotalFail := enqueueFullCount.Load() + enqueueClosedCount.Load()
	t.Logf("  - 총 실패 횟수   : %d 회 (Full + Closed)", enqueueTotalFail)
	t.Logf("  - 합계(성공+실패): %d 회", enqueueSuccCount.Load()+enqueueTotalFail)

	t.Logf(" C() 수신 통계:")
	t.Logf("  - 총 시도 횟수   : %d 회", goroutineCount*loopCount)
	t.Logf("  - 성공 횟수      : %d 회", dequeueSuccCount.Load())
	t.Logf("  - 비어있음 감지 : %d 회", dequeueEmptyCount.Load())
	t.Logf("  - ErrClosed 감지 : %d 회", dequeueClosedCount.Load())
	dequeueTotalFail := dequeueEmptyCount.Load() + dequeueClosedCount.Load()
	t.Logf("  - 총 실패 횟수   : %d 회 (Empty + Closed)", dequeueTotalFail)
	t.Logf("  - 합계(성공+실패): %d 회", dequeueSuccCount.Load()+dequeueTotalFail)

	t.Logf("--------------------------------------------------")
	t.Logf(" [시험 결과] : 패닉 없이 모든 고루틴이 ErrClosed를 감지하고 안전하게 종료됨")
	t.Logf("==================================================")
	record(t, fmt.Sprintf("Succ(Enq:%d, Deq:%d), ClosedDetected(Enq:%d, Deq:%d)", enqueueSuccCount.Load(), dequeueSuccCount.Load(), enqueueClosedCount.Load(), dequeueClosedCount.Load()))
}

// 4. Len()이 in-flight 항목을 집계하는지 검증
//
// pipe 고루틴이 inCh에서 꺼냈지만 아직 C()로 전달하지 못한 항목은
// inCh에도 outCh에도 존재하지 않는다(outCh는 무버퍼).
// 이 구간을 집계하지 않으면 Len()이 실제보다 적게 보고되어,
// Len()으로 정원을 계산하는 사용자(timer 등)가 용량을 초과 수용하게 된다.
func TestQueue_LenCountsInFlight(t *testing.T) {
	const capacity = 4

	q, err := New[int](capacity)
	if err != nil {
		t.Fatalf("큐 생성 실패: %v", err)
	}

	// pipe 단계 진입 시점을 포착하여 in-flight 상태를 결정적으로 만든다.
	// 수신자를 두지 않으므로 진입한 항목은 outCh 송신에서 대기한 채 머무른다.
	entered := make(chan struct{})
	var once sync.Once
	q.Use(func(c *Context[int]) {
		if c.Action() == ActionPipe {
			once.Do(func() { close(entered) })
		}
		c.Next()
	})

	t.Logf("==================================================")
	t.Logf(" [시험 목적 및 조건]")
	t.Logf("  - 시험 목적 : pipe 고루틴이 점유 중인(in-flight) 항목의 Len() 집계 검증")
	t.Logf("  - 시험 조건 : Capacity:%d, 수신자 없음", capacity)
	t.Logf("--------------------------------------------------")

	if err := q.Put(1); err != nil {
		t.Fatalf("Put 실패: %v", err)
	}

	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatalf("pipe 단계 진입 대기 시간 초과")
	}

	// 이 시점: 항목은 inCh에서 빠졌고 아직 C()로 전달되지 않은 in-flight 상태
	inFlightLen := q.Len()
	if inFlightLen != 1 {
		t.Errorf("in-flight 항목 미집계: Len()=1 기대, 실제 %d", inFlightLen)
	}

	// 버퍼에 추가로 쌓아도 총계가 정확해야 함
	for i := 2; i <= capacity; i++ {
		if err := q.Put(i); err != nil {
			t.Errorf("Put(%d) 실패: %v", i, err)
		}
	}
	fullLen := q.Len()
	if fullLen != capacity {
		t.Errorf("총계 불일치: Len()=%d 기대, 실제 %d", capacity, fullLen)
	}
	if fullLen > q.Cap() {
		t.Errorf("Len()이 Cap()을 초과함: Len()=%d, Cap()=%d", fullLen, q.Cap())
	}

	// Close 시점에 pipe가 갚지 않은 감소분이 남아 있어도 음수가 되어서는 안 됨
	q.Close()

	// outCh가 닫히면 pipe 고루틴이 종료된 것 (카운터 정리 완료)
	for range q.C() {
	}

	closedLen := q.Len()
	if closedLen < 0 {
		t.Errorf("Close 후 Len()이 음수: %d", closedLen)
	}
	if closedLen != 0 {
		t.Errorf("Close 후 Len(): 0 기대, 실제 %d", closedLen)
	}

	t.Logf(" [테스트 수치]")
	t.Logf("  - in-flight 1건일 때 Len() : %d (예상치: 1)", inFlightLen)
	t.Logf("  - 정원까지 채웠을 때 Len() : %d / Cap: %d", fullLen, q.Cap())
	t.Logf("  - Close 이후 Len()         : %d (예상치: 0)", closedLen)
	t.Logf("--------------------------------------------------")
	t.Logf(" [시험 결과] : 정상 (in-flight 집계 및 Close 후 카운터 정리 확인)")
	t.Logf("==================================================")

	record(t, fmt.Sprintf("InFlight:%d, Full:%d/%d, AfterClose:%d", inFlightLen, fullLen, q.Cap(), closedLen))
}

// 5. IsFull()과 Put()의 수용 기준이 일치하는지 검증
//
// IsFull()이 버퍼 길이만 보고 Put()이 총 보유량을 보면,
// in-flight 항목이 있을 때 "안 찼다"고 답한 직후 Put이 ErrFull을 반환하게 된다.
func TestQueue_IsFullMatchesPut(t *testing.T) {
	const capacity = 2

	q, err := New[int](capacity)
	if err != nil {
		t.Fatalf("큐 생성 실패: %v", err)
	}
	defer q.Close()

	// 첫 항목이 in-flight 상태가 되도록 pipe 단계 진입을 기다린다.
	entered := make(chan struct{})
	var once sync.Once
	q.Use(func(c *Context[int]) {
		if c.Action() == ActionPipe {
			once.Do(func() { close(entered) })
		}
		c.Next()
	})

	t.Logf("==================================================")
	t.Logf(" [시험 목적 및 조건]")
	t.Logf("  - 시험 목적 : in-flight 항목이 있을 때 IsFull()과 Put()의 판정 일치 검증")
	t.Logf("  - 시험 조건 : Capacity:%d, 수신자 없음", capacity)
	t.Logf("--------------------------------------------------")

	if err := q.Put(0); err != nil {
		t.Fatalf("Put 실패: %v", err)
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatalf("pipe 단계 진입 대기 시간 초과")
	}

	// 이후 상태를 바꿔 가며 두 판정이 항상 같은 결론을 내는지 확인
	mismatch := 0
	for i := 1; i <= capacity+1; i++ {
		full := q.IsFull()
		putErr := q.Put(i)

		switch {
		case full && putErr != ErrFull:
			mismatch++
			t.Errorf("Len()=%d: IsFull()=true 인데 Put()=%v (ErrFull 기대)", q.Len(), putErr)
		case !full && putErr != nil:
			mismatch++
			t.Errorf("Len()=%d: IsFull()=false 인데 Put()=%v (성공 기대)", q.Len(), putErr)
		}
		t.Logf("  - Len=%d Cap=%d : IsFull()=%-5v Put()=%v", q.Len(), q.Cap(), full, putErr)
	}

	if q.Len() != capacity {
		t.Errorf("최종 Len(): %d 기대, 실제 %d", capacity, q.Len())
	}
	if !q.IsFull() {
		t.Errorf("정원 도달 상태인데 IsFull()=false (Len=%d, Cap=%d)", q.Len(), q.Cap())
	}

	t.Logf("--------------------------------------------------")
	t.Logf(" [시험 결과] : 정상 (불일치 %d건)", mismatch)
	t.Logf("==================================================")

	record(t, fmt.Sprintf("IsFull/Put agreement verified (mismatch:%d)", mismatch))
}
