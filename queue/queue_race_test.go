package queue

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestQueueConcurrencyRace(t *testing.T) {
	// 1. 용량이 정확히 1개인 큐를 생성합니다. (가장 경합이 치열한 환경)
	q, err := New[int](1)
	if err != nil {
		t.Fatalf("큐 생성 실패: %v", err)
	}

	const (
		producerCount = 50   // 동시에 데이터를 밀어 넣을 생산자 고루틴 수
		consumerCount = 50   // 동시에 데이터를 빼갈 소비자 고루틴 수
		iterations    = 1000 // 고루틴당 시도 횟수
	)

	var (
		successEnqueue int64 // Enqueue에 성공한 총 횟수
		successDequeue int64 // Dequeue에 성공한 총 횟수
		failedFull     int64 // 큐가 가득 차서 실패한(Full) 횟수
		failedEmpty    int64 // 큐가 비어서 실패한(Empty) 횟수
	)

	var wg sync.WaitGroup

	// --- [생산자 고루틴 50개 구동] ---
	for i := 0; i < producerCount; i++ {
		wg.Add(1)
		go func(pID int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				// 고루틴 경합 타이밍을 흔들기 위해 미세한 찰나의 시차를 둡니다.
				if j%10 == 0 {
					time.Sleep(time.Nanosecond)
				}

				// Enqueue 난타
				err := q.Enqueue(pID*1000 + j)
				if err == nil {
					atomic.AddInt64(&successEnqueue, 1)
				} else if err == QueueErrEnqueueFull {
					atomic.AddInt64(&failedFull, 1)
				}
			}
		}(i)
	}

	// --- [소비자 고루틴 50개 구동] ---
	for i := 0; i < consumerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				if j%10 == 0 {
					time.Sleep(time.Nanosecond)
				}

				// Dequeue 난타
				_, err := q.Dequeue()
				if err == nil {
					atomic.AddInt64(&successDequeue, 1)
				} else if err == QueueErrDequeueEmpty {
					atomic.AddInt64(&failedEmpty, 1)
				}
			}
		}()
	}

	// 모든 고루틴이 난타전을 끝낼 때까지 대기
	wg.Wait()

	// --- [검증 로직] ---
	t.Logf("=== 1개짜리 큐 난타전 결과 ===")
	t.Logf("생산자(Enqueue) 성공: %d 번, 실패(Full): %d 번", successEnqueue, failedFull)
	t.Logf("소비자(Dequeue) 성공: %d 번, 실패(Empty): %d 번", successDequeue, failedEmpty)

	// 💡 물리 법칙 검증: 넌블로킹 환경이더라도, 성공하여 큐에 '들어간' 개수는 '나온' 개수와 일치하거나
	// 마지막에 딱 1개(capacity=1 이므로) 채널 버퍼에 남아있는 상태여야 합니다.
	remainingInQueue := int64(q.Len())
	if successEnqueue != (successDequeue + remainingInQueue) {
		t.Errorf("🚨 데이터 불일치 지뢰 발생! 들어간 개수(%d) != 나온 개수(%d) + 잔여(%d)",
			successEnqueue, successDequeue, remainingInQueue)
	} else {
		t.Logf("✅ 데이터 무결성 검증 통과! 오염되거나 유실된 데이터 없음.")
	}
}

func TestQueue_AbruptClose(t *testing.T) {
	// 1. 적당한 버퍼 크기를 가진 큐를 생성합니다.
	const capacity = 10
	q, err := New[int](capacity)
	if err != nil {
		t.Fatalf("큐 생성 실패: %v", err)
	}

	const (
		goroutineCount = 100
		loopCount      = 1000
	)

	var (
		enqueueSucc   atomic.Uint64
		enqueueClosed atomic.Uint64 // Close 이후 입력 차단(에러)된 횟수
		enqueueFull   atomic.Uint64
		dequeueSucc   atomic.Uint64
		dequeueClosed atomic.Uint64 // Close 이후 수근 차단(에러)된 횟수
		dequeueEmpty  atomic.Uint64
	)

	startSignal := make(chan struct{})
	var wg sync.WaitGroup

	// --- [생산자 고루틴 100개] ---
	for i := 0; i < goroutineCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-startSignal

			for j := 0; j < loopCount; j++ {
				err := q.Enqueue(j)
				if err == nil {
					enqueueSucc.Add(1)
				} else if err == QueueErrClosed {
					enqueueClosed.Add(1) // 💡 닫힌 후 에러를 정상 포획한 경우
				} else if err == QueueErrEnqueueFull {
					enqueueFull.Add(1)
				}
				// 극단적인 난타 타이밍을 위해 CPU 스케줄링을 의도적으로 흔듦
				runtime.Gosched()
			}
		}()
	}

	// --- [소비자 고루틴 100개] ---
	for i := 0; i < goroutineCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-startSignal

			for j := 0; j < loopCount; j++ {
				_, err := q.Dequeue()
				if err == nil {
					dequeueSucc.Add(1)
				} else if err == QueueErrClosed {
					dequeueClosed.Add(1) // 💡 닫힌 후 에러를 정상 포획한 경우
				} else if err == QueueErrDequeueEmpty {
					dequeueEmpty.Add(1)
				}
				runtime.Gosched()
			}
		}()
	}

	// 동시에 출발 시그널 투척
	close(startSignal)

	// 🔥 [핵심 시나리오] 난타전이 최고조에 달할 때까지 아주 찰나의 시간(5밀리초)만 준 뒤,
	// 기습적으로 문을 걸어 잠급니다.
	time.Sleep(5 * time.Millisecond)
	t.Logf("⚡ [기습] 고루틴들이 폭주하는 와중에 큐를 강제로 Close() 합니다!")
	q.Close()

	// 모든 고루틴이 정리가 끝나서 탈출할 때까지 대기
	wg.Wait()

	t.Logf("==================================================")
	t.Logf(" 💥 기습 셧다운 결과 통계")
	t.Logf("--------------------------------------------------")
	t.Logf(" Enqueue -> 성공: %d, 가득참: %d, [정상차단(Closed)]: %d",
		enqueueSucc.Load(), enqueueFull.Load(), enqueueClosed.Load())
	t.Logf(" Dequeue -> 성공: %d, 비어있음: %d, [정상차단(Closed)]: %d",
		dequeueSucc.Load(), dequeueEmpty.Load(), dequeueClosed.Load())
	t.Logf("==================================================")

	// 💡 최종 검증: 닫힌 이후에는 Enqueue/Dequeue 하려던 수많은 고루틴들이
	// 패닉으로 터지지 않고 최소한 몇 번 이상은 "QueueErrClosed" 에러를 정상적으로 받아 쥐고 탈출했어야 합니다.
	if enqueueClosed.Load() == 0 && dequeueClosed.Load() == 0 {
		t.Errorf("🚨 경고: 큐가 제대로 닫히지 않았거나, Closed 에러를 포획하지 못했습니다.")
	} else {
		t.Logf("✅ 검증 통과: 패닉 크래시 없이 모든 고루틴이 Closed 에러를 인지하고 우아하게 종료됨.")
	}
}
