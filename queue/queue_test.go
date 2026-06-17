package queue

import (
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// 1. capacity가 1인 queue에 멀티고루틴에서 Enqueue, Dequeue를 반복하고 정상 확인
func TestQueue_Concurrency(t *testing.T) {
	const capacity = 1
	const workers = 50
	const itemsPerWorker = 20

	q, _ := New[int](capacity)
	var enqSucc, deqSucc atomic.Int64
	var wg sync.WaitGroup

	// Enqueuer 고루틴들
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < itemsPerWorker; j++ {
				val := id*itemsPerWorker + j
				for q.Enqueue(val) != nil {
					// 큐가 꽉 찼으면 CPU 양보
					runtime.Gosched()
				}
				enqSucc.Add(1)
			}
		}(i)
	}

	// Dequeuer 고루틴들
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < itemsPerWorker; j++ {
				for _, err := q.Dequeue(); err != nil; _, err = q.Dequeue() {
					// 큐가 비었으면 CPU 양보
					runtime.Gosched()
				}
				deqSucc.Add(1)
			}
		}()
	}

	wg.Wait()

	if enqSucc.Load() != deqSucc.Load() {
		t.Errorf("Enqueue와 Dequeue 성공 횟수가 다릅니다: Enq=%d, Deq=%d", enqSucc.Load(), deqSucc.Load())
	}
	if q.Len() != 0 {
		t.Errorf("테스트 종료 후 큐에 데이터가 남아있습니다: Len=%d", q.Len())
	}
	t.Logf("Concurrency Test: Enq/Deq Success Count: %d", enqSucc.Load())
}

// 2. capacity가 1인 queue에 멀티고루틴에서 C()를 사용하고, 다른 멀티 고루틴에서 Enqueue를 반복하고 정상 확인
func TestQueue_ChannelConcurrency(t *testing.T) {
	const capacity = 1
	const workers = 50
	const itemsPerWorker = 20

	q, _ := New[int](capacity)
	var enqSucc, deqSucc atomic.Int64
	var wg sync.WaitGroup

	// Enqueuer 고루틴들
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < itemsPerWorker; j++ {
				val := id*itemsPerWorker + j
				for q.Enqueue(val) != nil {
					runtime.Gosched()
				}
				enqSucc.Add(1)
			}
		}(i)
	}

	// Consumer 고루틴 (C() 채널 사용)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch := q.C()
			for j := 0; j < itemsPerWorker; j++ {
				<-ch
				deqSucc.Add(1)
			}
		}()
	}

	wg.Wait()

	if enqSucc.Load() != deqSucc.Load() {
		t.Errorf("Enqueue와 C()를 통한 소비 횟수가 다릅니다: Enq=%d, Deq=%d", enqSucc.Load(), deqSucc.Load())
	}
	if q.Len() != 0 {
		t.Errorf("테스트 종료 후 큐에 데이터가 남아있습니다: Len=%d", q.Len())
	}
	t.Logf("Channel Concurrency Test: Enq/Deq Success Count: %d", enqSucc.Load())
}

// 3. 멀티 고루틴에서 Enqueue(), Dequeue(), C()를 사용하는 중에 Close를 함수를 호출해도 문제가 없는지 확인
func TestQueue_CloseSafety(t *testing.T) {
	const capacity = 100
	const workers = 20

	q, _ := New[int](capacity)
	var wg sync.WaitGroup
	stop := make(chan struct{})

	// 다양한 작업을 하는 고루틴들 생성
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(id int) { // Enqueuer
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					err := q.Enqueue(id)
					if errors.Is(err, ErrClosed) {
						return
					}
				}
			}
		}(i)

		wg.Add(1)
		go func() { // Dequeuer
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_, err := q.Dequeue()
					if errors.Is(err, ErrClosed) {
						return
					}
				}
			}
		}()

		wg.Add(1)
		go func() { // Consumer from C()
			defer wg.Done()
			ch := q.C()
			for {
				select {
				case <-stop:
					return
				case _, ok := <-ch:
					if !ok {
						return
					}
				}
			}
		}()
	}

	// 잠시 작업 진행
	time.Sleep(50 * time.Millisecond)

	// 작업 도중 Close 호출
	q.Close()
	close(stop) // 작업 고루틴들 종료 신호
	wg.Wait()

	// Close 이후 작업이 ErrClosed를 반환하는지 확인
	if err := q.Enqueue(1); !errors.Is(err, ErrClosed) {
		t.Errorf("expected ErrClosed after Close, got: %v", err)
	}
	if _, err := q.Dequeue(); !errors.Is(err, ErrClosed) {
		t.Errorf("expected ErrClosed after Close, got: %v", err)
	}

	t.Log("Close Safety Test: 패닉 없이 정상 종료됨")
}
