package queue

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type myStruct struct {
	myData [1024 * 1024 * 10]byte
}

func TestQueue_Test1(t *testing.T) {
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
	t.Logf(" 1. 시험 시작: cap:%d개 고루틴:%d개 반복:%d회 Dequeue/Enqueue 시험", capacity, goroutineCount*2, loopCount)
	close(startSignal)

	wg.Wait()

	t.Logf("==================================================")
	t.Logf(" 2. 통계")
	t.Logf("--------------------------------------------------")
	t.Logf(" 총 Dequeue 시도 : %d 회", goroutineCount*loopCount)
	t.Logf("  - 성공 : %d 회", dequeueSuccCount.Load())
	t.Logf("  - 실패 : %d 회", dequeueFailCount.Load())
	t.Logf("--------------------------------------------------")
	t.Logf(" 총 Enqueue 시도 : %d 회", goroutineCount*loopCount)
	t.Logf("  - 성공 : %d 회", enqueueSuccCount.Load())
	t.Logf("  - 실패 : %d 회", enqueueFailCount.Load())

	t.Logf("==================================================")
	if (dequeueSuccCount.Load() + dequeueFailCount.Load()) != (goroutineCount * loopCount) {
		t.Errorf("자원 누수 발생: 대여 성공 횟수(%d)와 반납 성공 횟수(%d)가 불일치합니다!", dequeueSuccCount.Load(), dequeueFailCount.Load())
	} else if (enqueueSuccCount.Load() + enqueueFailCount.Load()) != (goroutineCount * loopCount) {
		t.Errorf("자원 누수 발생: 대여 성공 횟수(%d)와 반납 성공 횟수(%d)가 불일치합니다!", enqueueSuccCount.Load(), enqueueFailCount.Load())
	} else {
		t.Logf(" 3. 결과: 정상 (Get 총 %d 회 / Put 총 %d 회)", dequeueSuccCount.Load()+dequeueFailCount.Load(), enqueueSuccCount.Load()+enqueueFailCount.Load())
	}

	// queue Close
	q = nil

	for i := 0; i < 1; i++ {
		runtime.GC()
		time.Sleep(time.Microsecond * 30)
	}

	// Close 후 메모리 사용량
	runtime.ReadMemStats(&msClose)

	t.Logf("==================================================")
	t.Logf(" 4. 메모리 사용량: %.2f MB -> %.2f MB -> %.2f MB",
		float64(msBefore.Alloc)/(1024*1024), float64(msNew.Alloc)/(1024*1024), float64(msClose.Alloc)/(1024*1024))

	t.Logf("--------------------------------------------------")
	if (int64(msClose.Alloc) - int64(msBefore.Alloc)) > (1 * 1024 * 1024) {
		t.Errorf("자원 정리 실패: Close() 이후 시간이 흘렀음에도 %.2f MB가 해제되지 못했습니다.", float64(msClose.Alloc-msBefore.Alloc)/(1024*1024))
	} else {
		t.Logf("  - 메모리 해제 정상 (1 MB 오차 범위)")
	}
	t.Logf("==================================================")
}
