package mempool

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

func TestMemPool_Test1(t *testing.T) {
	const capacity = 1
	const goroutineCount = 1000

	var msBefore runtime.MemStats
	var msNew runtime.MemStats
	var msClose runtime.MemStats
	var beforeMem uint64
	var newMem uint64
	var closeMem uint64

	var getSuccCount atomic.Uint64
	var getFailCount atomic.Uint64
	var putSuccCount atomic.Uint64
	var putFailCount atomic.Uint64
	var putFailDupKeyCount atomic.Uint64
	var putFailWrongKeyCount atomic.Uint64

	var wg sync.WaitGroup

	// 초기 메모리 사용량
	runtime.GC()
	runtime.ReadMemStats(&msBefore)
	beforeMem = msBefore.Alloc

	// mempool.New
	mpool, err := New[myStruct](capacity)
	if err != nil {
		t.Fatalf("mempool 초기화 실패: %+v", err)
	}

	// 생성 후 메모리 사용량
	runtime.GC()
	runtime.ReadMemStats(&msNew)
	newMem = msNew.Alloc

	startSignal := make(chan any)

	// 1. 멀티 고루틴에서 Get/Put 시험
	for i := 0; i < goroutineCount; i++ {
		// Get/Put 경합 고루틴
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			<-startSignal

			for j := 0; j < 10; j++ {
				// Get 시험
				_, key, err := mpool.Get()
				if err != nil {
					getFailCount.Add(1)
					continue
				}
				getSuccCount.Add(1)

				// 다른 고루틴에게 CPU 스케줄링 양보 유도
				runtime.Gosched()

				// Put 시험
				err = mpool.Put(key)
				if err != nil {
					putFailCount.Add(1)
				} else {
					putSuccCount.Add(1)
				}

				// 중복 Put 시험
				err = mpool.Put(key)
				if err != nil {
					putFailDupKeyCount.Add(1)
				} else {
					putSuccCount.Add(1)
				}
			}
		}(i)

		// 잘못된 key로 Put 시험
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			<-startSignal

			for j := 0; j < 10; j++ {
				wrongKey := packKey(i+j, uint32((id+i+j)*7))
				err := mpool.Put(wrongKey)
				if err != nil {
					putFailWrongKeyCount.Add(1)
				} else {
					t.Errorf("잘못된 키로 Put 함수가 처리됨!!! (id:%d key:%x)", id, wrongKey)
				}
			}
		}(i)
	}

	t.Logf("==================================================")
	t.Logf(" 1. 시험 시작: cap:%d개 고루틴:%d개 Get/Put 시험", capacity, goroutineCount*2)
	t.Logf("==================================================")
	close(startSignal)

	wg.Wait()

	t.Logf(" 2. 통계")
	t.Logf("--------------------------------------------------")
	t.Logf(" 총 Get 시도 : %d 회", goroutineCount*10)
	t.Logf("  - 성공 : %d 회", getSuccCount.Load())
	t.Logf("  - 실패 : %d 회", getFailCount.Load())
	t.Logf("--------------------------------------------------")
	t.Logf(" 총 Put 시도 : %d 회", (getSuccCount.Load()*2)+(goroutineCount*10)*1)
	t.Logf("  - 성공 : %d 회", putSuccCount.Load())
	t.Logf("  - 실패 : %d 회", putFailCount.Load())
	t.Logf("  - 중복키 차단 성공: %d 회", putFailDupKeyCount.Load())
	t.Logf("  - 가짜키 차단 성공: %d 회", putFailWrongKeyCount.Load())
	t.Logf("==================================================")

	if getSuccCount.Load() != putSuccCount.Load() {
		t.Errorf("자원 누수 발생: 대여 성공 횟수(%d)와 반납 성공 횟수(%d)가 불일치합니다!", getSuccCount.Load(), putSuccCount.Load())
	} else {
		t.Logf(" 3. 결과: 정상 (Get 성공 %d 회 / Put 성공 %d 회)", getSuccCount.Load(), putSuccCount.Load())
	}
	t.Logf("==================================================")

	// 현재 풀 안에 자원이 들어있는지 (다시 Get이 되는지) 확인
	mem, _, err := mpool.Get()
	if err != nil {
		t.Errorf("자원 소멸: 경합이 끝난 후 풀에 자원이 유실되어 비어있습니다: %v", err)
	} else if mem == nil {
		t.Errorf("메모리 오염: 자원은 반환되었으나 메모리 포인터가 오염되었습니다.")
	}

	// mempool Close
	if err := mpool.Close(); err != nil {
		t.Fatalf("Close 실패: %+v", err)
	}

	for i := 0; i < 1; i++ {
		runtime.GC()
		time.Sleep(time.Microsecond * 30)
	}

	// Close 후 메모리 사용량
	runtime.ReadMemStats(&msClose)
	closeMem = msClose.Alloc

	t.Logf("==================================================")
	t.Logf(" 4. 메모리 사용량: %.2f MB -> %.2f MB -> %.2f MB",
		float64(beforeMem)/(1024*1024), float64(newMem)/(1024*1024), float64(closeMem)/(1024*1024))

	t.Logf("--------------------------------------------------")
	if (int64(closeMem) - int64(beforeMem)) > (1 * 1024 * 1024) {
		t.Errorf("자원 정리 실패: Close() 이후 시간이 흘렀음에도 %.2f MB가 해제되지 못했습니다.", float64(closeMem-beforeMem)/(1024*1024))
	} else {
		t.Logf("  - 메모리 해제 정상 (1 MB 오차 범위)")
	}
	t.Logf("==================================================")
}
