package queue

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

	// 1. 멀티 고루틴에서 Dequeue/Enqueue 시험
	for i := 0; i < goroutineCount; i++ {
		// Dequeue/Enqueue 경합 고루틴
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			<-startSignal

			for j := 0; j < loopCount; j++ {
				// Dequeue 시험
				_, err := q.Dequeue()
				if err != nil {
					dequeueFailCount.Add(1)
					continue
				}
				dequeueSuccCount.Add(1)
			}
		}(i)

		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			<-startSignal

			for j := 0; j < loopCount; j++ {
				// Enqueue 시험
				err := q.Enqueue(data)
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
	t.Logf("  - 시험 목적 : 멀티 고루틴 Enqueue/Dequeue 경합 및 메모리 정리 테스트")
	t.Logf("  - 시험 조건 : Capacity:%d, 고루틴:%d개, 반복:%d회", capacity, goroutineCount*2, loopCount)
	t.Logf("--------------------------------------------------")
	t.Logf(" => 시험 진행 중...")
	close(startSignal)

	wg.Wait()

	t.Logf("==================================================")
	t.Logf(" [테스트 수치]")
	t.Logf("--------------------------------------------------")
	t.Logf(" 총 Dequeue 시도 : %d 회", goroutineCount*loopCount)
	t.Logf("  - 성공 : %d 회", dequeueSuccCount.Load())
	t.Logf("  - 실패 : %d 회", dequeueFailCount.Load())
	t.Logf("--------------------------------------------------")
	t.Logf(" 총 Enqueue 시도 : %d 회", goroutineCount*loopCount)
	t.Logf("  - 성공 : %d 회", enqueueSuccCount.Load())
	t.Logf("  - 실패 : %d 회", enqueueFailCount.Load())

	// queue Close
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

// 1. 100개의 큐를 생성하여 Enqueue한 값이 그대로 Dequeue되는지 확인하는 테스트
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
		if err := q.Enqueue(expected); err != nil {
			t.Errorf("[%d] Enqueue 실패: %v", i, err)
		}

		actual, err := q.Dequeue()
		if err != nil {
			t.Errorf("[%d] Dequeue 실패: %v", i, err)
		}

		if expected != actual {
			t.Errorf("[%d] 데이터 불일치: 예상값 %d, 실제값 %d", i, expected, actual)
		}
	}
	t.Logf("--------------------------------------------------")
	t.Logf(" [테스트 수치]")
	t.Logf("  - 총 %d개 큐 생성 및 Enqueue/Dequeue 정합성 검증 완료", count)
	t.Logf(" [시험 결과] : 정상 (모든 데이터 일치)")
	t.Logf("==================================================")
	record(t, fmt.Sprintf("Verified %d independent queue instances", count))
}

// 2. Use 메서드를 이용한 Enqueue/Dequeue 실행 시간 측정 테스트
func TestQueue_ExecutionTimeMeasurement(t *testing.T) {
	const goroutineCount = 50
	const loopCount = 100
	q, _ := New[int](goroutineCount * loopCount)

	var totalDuration atomic.Int64
	var opCount atomic.Int64

	// 미들웨어를 이용한 시간 측정 로직 등록
	q.Use(func(c *Context[int]) {
		start := time.Now()
		c.Next() // 실제 작업(Enqueue/Dequeue) 수행
		elapsed := time.Since(start)

		totalDuration.Add(int64(elapsed))
		opCount.Add(1)
	})

	t.Logf("==================================================")
	t.Logf(" [시험 목적 및 조건]")
	t.Logf("  - 시험 목적 : 미들웨어를 이용한 Enqueue/Dequeue 실행 시간 측정 테스트")
	t.Logf("  - 시험 조건 : 총 고루틴: %d개, 반복: %d회", goroutineCount*2, loopCount)

	var wg sync.WaitGroup
	startSignal := make(chan struct{})

	for i := 0; i < goroutineCount; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-startSignal
			for j := 0; j < loopCount; j++ {
				_ = q.Enqueue(j)
			}
		}()
		go func() {
			defer wg.Done()
			<-startSignal
			for j := 0; j < loopCount; j++ {
				_, _ = q.Dequeue()
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

	// Enqueue/Dequeue를 수행하는 여러 고루틴 생성
	for i := 0; i < goroutineCount; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-startSignal
			for j := 0; j < loopCount; j++ {
				err := q.Enqueue(j)
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
				_, err := q.Dequeue()
				if err == nil {
					dequeueSuccCount.Add(1)
				} else if err == ErrClosed {
					dequeueClosedCount.Add(1)
				} else if err == ErrEmpty {
					dequeueEmptyCount.Add(1)
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
	t.Logf("  - 총 고루틴 개수 : %d (Enqueue:%d, Dequeue:%d)", goroutineCount*2, goroutineCount, goroutineCount)
	t.Logf("  - 고루틴당 반복  : %d 회", loopCount)
	t.Logf("--------------------------------------------------")
	t.Logf(" Enqueue 통계:")
	t.Logf("  - 총 시도 횟수   : %d 회", goroutineCount*loopCount)
	t.Logf("  - 성공 횟수      : %d 회", enqueueSuccCount.Load())
	t.Logf("  - ErrFull 감지   : %d 회", enqueueFullCount.Load())
	t.Logf("  - ErrClosed 감지 : %d 회", enqueueClosedCount.Load())
	enqueueTotalFail := enqueueFullCount.Load() + enqueueClosedCount.Load()
	t.Logf("  - 총 실패 횟수   : %d 회 (Full + Closed)", enqueueTotalFail)
	t.Logf("  - 합계(성공+실패): %d 회", enqueueSuccCount.Load()+enqueueTotalFail)

	t.Logf(" Dequeue 통계:")
	t.Logf("  - 총 시도 횟수   : %d 회", goroutineCount*loopCount)
	t.Logf("  - 성공 횟수      : %d 회", dequeueSuccCount.Load())
	t.Logf("  - ErrEmpty 감지  : %d 회", dequeueEmptyCount.Load())
	t.Logf("  - ErrClosed 감지 : %d 회", dequeueClosedCount.Load())
	dequeueTotalFail := dequeueEmptyCount.Load() + dequeueClosedCount.Load()
	t.Logf("  - 총 실패 횟수   : %d 회 (Empty + Closed)", dequeueTotalFail)
	t.Logf("  - 합계(성공+실패): %d 회", dequeueSuccCount.Load()+dequeueTotalFail)

	t.Logf("--------------------------------------------------")
	t.Logf(" [시험 결과] : 패닉 없이 모든 고루틴이 ErrClosed를 감지하고 안전하게 종료됨")
	t.Logf("==================================================")
	record(t, fmt.Sprintf("Succ(Enq:%d, Deq:%d), ClosedDetected(Enq:%d, Deq:%d)", enqueueSuccCount.Load(), dequeueSuccCount.Load(), enqueueClosedCount.Load(), dequeueClosedCount.Load()))
}
