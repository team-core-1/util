package indexmempool

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/team-core-1/util/internal/testreport"
)

func TestMain(m *testing.M) { testreport.Main(m, "indexmempool") }

// 콜백이 오래 걸릴 때 블로킹/논블로킹을 구분하기 위한 기준 시간.
// 실제 대기가 이 값에 근접하면 블로킹, 훨씬 짧으면 논블로킹으로 본다.
const holdTime = 100 * time.Millisecond

// ---------------------------------------------------------------------------
// T1. 생성과 용량
// ---------------------------------------------------------------------------

func TestPool_NewAndCap(t *testing.T) {
	r := testreport.New(t, "New의 용량 검증과 생성 직후 상태 확인", "Capacity 5")
	defer r.Done()

	_, err := New[int](0)
	r.CheckErr(err, ErrInvalidCap, "New(0)", "ErrInvalidCap")

	_, err = New[int](-1)
	r.CheckErr(err, ErrInvalidCap, "New(-1)", "ErrInvalidCap")

	ip, err := New[int](5)
	if err != nil {
		t.Fatalf("New(5) 실패: %v", err)
	}
	// Len은 남은 여유 슬롯 수이므로 생성 직후에는 Cap과 같다.
	r.Check(ip.Len() == 5 && ip.Cap() == 5, "생성 직후 상태", "Len=5 Cap=5 (전부 여유)",
		fmt.Sprintf("Len=%d Cap=%d", ip.Len(), ip.Cap()))

	idx, err := ip.Get()
	r.Check(err == nil && idx >= 0 && idx < 5, "첫 Get의 인덱스 범위", "0 이상 Cap 미만",
		fmt.Sprintf("idx=%d err=%v", idx, err))
}

// ---------------------------------------------------------------------------
// T2. 기본 연산 정상 경로
// ---------------------------------------------------------------------------

func TestPool_BasicOps(t *testing.T) {
	r := testreport.New(t, "Get-Access-Put 사이클의 정상 동작 확인", "Capacity 3, 단일 고루틴")
	defer r.Done()

	ip, _ := New[string](3)

	idx, err := ip.Get()
	r.Check(err == nil, "Get", "슬롯 할당 성공", fmt.Sprintf("err=%v", err))

	err = ip.Access(idx, func(m *string) { *m = "hello" })
	r.CheckErr(err, nil, "Access 쓰기", "성공")

	var got string
	err = ip.Access(idx, func(m *string) { got = *m })
	r.Check(err == nil && got == "hello", "Access 읽기", "쓴 값이 그대로 조회됨",
		fmt.Sprintf("err=%v value=%q", err, got))

	err = ip.AccessLock(idx, func(m *string) { *m += " world" })
	r.CheckErr(err, nil, "AccessLock 쓰기", "성공")

	_ = ip.AccessLock(idx, func(m *string) { got = *m })
	r.Check(got == "hello world", "Access/AccessLock 동일 슬롯", "두 경로가 같은 메모리를 다룸",
		fmt.Sprintf("value=%q", got))

	r.CheckErr(ip.Put(idx), nil, "Put", "반납 성공")

	// 반납한 슬롯은 제로값으로 초기화되어 재할당된다.
	//
	// 반납분은 재사용 대기열 맨 뒤로 가므로, 바로 다음 Get은 다른 슬롯을 준다.
	// 나머지를 모두 소진해 반납한 그 슬롯을 되받아야 초기화 여부를 검증할 수 있다.
	var reused string
	reclaimed := false
	for {
		next, err := ip.Get()
		if err != nil {
			break
		}
		if next == idx {
			_ = ip.Access(next, func(m *string) { reused = *m })
			reclaimed = true
			break
		}
	}
	r.Check(reclaimed && reused == "", "반납 후 초기화",
		fmt.Sprintf("슬롯 %d를 되받았을 때 제로값", idx),
		fmt.Sprintf("되받음=%v, 남은 값=%q", reclaimed, reused))
}

// ---------------------------------------------------------------------------
// T3. 에러 경로 전수
// ---------------------------------------------------------------------------

func TestPool_Errors(t *testing.T) {
	r := testreport.New(t, "정의된 에러 7종이 정확한 조건에서 반환되는지", "Capacity 2, 단일 고루틴")
	defer r.Done()

	ip, _ := New[int](2)

	// 범위를 벗어난 인덱스
	e1 := ip.Put(-1)
	e2 := ip.Access(2, func(*int) {})
	e3 := ip.AccessLock(99, func(*int) {})
	r.Check(e1 == ErrInvalidIndex && e2 == ErrInvalidIndex && e3 == ErrInvalidIndex,
		"범위 밖 인덱스", "Put/Access/AccessLock 모두 ErrInvalidIndex",
		fmt.Sprintf("put=%v access=%v accessLock=%v", e1, e2, e3))

	// 할당되지 않은 슬롯
	e1 = ip.Put(0)
	e2 = ip.Access(0, func(*int) {})
	e3 = ip.AccessLock(0, func(*int) {})
	r.Check(e1 == ErrNotAllocatedIndex && e2 == ErrNotAllocatedIndex && e3 == ErrNotAllocatedIndex,
		"미할당 슬롯", "Put/Access/AccessLock 모두 ErrNotAllocatedIndex",
		fmt.Sprintf("put=%v access=%v accessLock=%v", e1, e2, e3))

	idx, _ := ip.Get()

	// nil 콜백 (범위 검사가 먼저 수행된다)
	e1 = ip.Access(idx, nil)
	e2 = ip.AccessLock(idx, nil)
	r.Check(e1 == ErrNilCallback && e2 == ErrNilCallback, "nil 콜백",
		"Access/AccessLock 모두 ErrNilCallback",
		fmt.Sprintf("access=%v accessLock=%v", e1, e2))
	r.CheckErr(ip.Access(-1, nil), ErrInvalidIndex, "범위 밖 + nil 콜백",
		"ErrInvalidIndex (범위 검사 우선)")

	// 풀 고갈
	_, _ = ip.Get()
	_, err := ip.Get()
	r.CheckErr(err, ErrEmpty, "풀 고갈 시 Get", "ErrEmpty")

	// 중복 반납
	r.CheckErr(ip.Put(idx), nil, "정상 반납", "성공")
	r.CheckErr(ip.Put(idx), ErrNotAllocatedIndex, "중복 반납", "ErrNotAllocatedIndex")
}

// ---------------------------------------------------------------------------
// T4. nil 리시버 안전성
// ---------------------------------------------------------------------------

func TestPool_NilReceiver(t *testing.T) {
	r := testreport.New(t, "nil 풀에 대한 모든 공개 메서드가 패닉 없이 방어되는지", "초기화하지 않은 *Pool 포인터")
	defer r.Done()

	var ip *Pool[int]

	idx, err := ip.Get()
	r.Check(err == ErrNil && idx == -1, "Get", "ErrNil, 인덱스 -1 반환",
		fmt.Sprintf("idx=%d err=%v", idx, err))

	r.CheckErr(ip.Put(0), ErrNil, "Put", "ErrNil")
	r.CheckErr(ip.Access(0, func(*int) {}), ErrNil, "Access", "ErrNil")
	r.CheckErr(ip.AccessLock(0, func(*int) {}), ErrNil, "AccessLock", "ErrNil")
	r.Check(ip.Len() == 0 && ip.Cap() == 0, "Len/Cap", "둘 다 0",
		fmt.Sprintf("Len=%d Cap=%d", ip.Len(), ip.Cap()))

	safe := func() (ok bool) {
		defer func() { ok = recover() == nil }()
		ip.Use(func(c *Context[int]) {})
		return
	}()
	r.Check(safe, "Use", "패닉 없음", "패닉 발생")
}

// ---------------------------------------------------------------------------
// T5. 여유 슬롯과 용량
//
// Len은 저장 개수가 아니라 "남은 여유 슬롯 수"다.
// 사용 중인 개수는 Cap() - Len()으로 구한다.
// ---------------------------------------------------------------------------

func TestPool_LenCap(t *testing.T) {
	r := testreport.New(t, "Get/Put에 따른 여유 슬롯 수 변화 확인", "Capacity 4")
	defer r.Done()

	ip, _ := New[int](4)

	r.Check(ip.Len() == 4 && ip.Cap() == 4, "초기 상태", "Len=4 (전부 여유)",
		fmt.Sprintf("Len=%d Cap=%d", ip.Len(), ip.Cap()))

	idxs := make([]int, 0, 3)
	for i := 0; i < 3; i++ {
		idx, err := ip.Get()
		if err != nil {
			t.Fatalf("Get 실패: %v", err)
		}
		idxs = append(idxs, idx)
	}
	r.Check(ip.Len() == 1, "3건 할당 후", "Len=1 (여유 감소)", fmt.Sprintf("Len=%d", ip.Len()))
	r.Check(ip.Cap()-ip.Len() == 3, "사용 중 개수", "Cap-Len=3",
		fmt.Sprintf("Cap-Len=%d", ip.Cap()-ip.Len()))

	_ = ip.Put(idxs[0])
	r.Check(ip.Len() == 2, "1건 반납 후", "Len=2", fmt.Sprintf("Len=%d", ip.Len()))

	r.Check(ip.Cap() == 4, "Cap 불변", "할당·반납과 무관하게 4", fmt.Sprintf("Cap=%d", ip.Cap()))

	// 전부 할당하면 고갈되고, 반납하면 다시 쓸 수 있다.
	for ip.Len() > 0 {
		if _, err := ip.Get(); err != nil {
			break
		}
	}
	_, err := ip.Get()
	r.Check(ip.Len() == 0 && err == ErrEmpty, "전부 할당", "Len=0, 추가 Get은 ErrEmpty",
		fmt.Sprintf("Len=%d err=%v", ip.Len(), err))

	_ = ip.Put(idxs[1])
	_, err = ip.Get()
	r.Check(err == nil, "반납 후 재할당", "반납한 자리를 다시 사용 가능", fmt.Sprintf("err=%v", err))
}

// ---------------------------------------------------------------------------
// T6. Access와 AccessLock의 동시 접근 의미론
//
// Access는 콜백 실행 전에 슬롯 락을 해제하고 사용 중 표시만 남긴다(논블로킹 거부).
// AccessLock은 콜백 구간 전체에서 슬롯 락을 유지한다(블로킹 직렬화).
// 두 방식을 섞어 쓸 때 서로의 상태를 훼손하지 않아야 한다.
// ---------------------------------------------------------------------------

func TestPool_AccessSemantics(t *testing.T) {
	r := testreport.New(t, "Access(논블로킹)와 AccessLock(블로킹)의 조합별 동작 확인",
		fmt.Sprintf("Capacity 1, 콜백 점유 %v", holdTime))
	defer r.Done()

	// ── Access 실행 중 → Access / AccessLock : 즉시 거부
	ip, _ := New[int](1)
	idx, _ := ip.Get()

	entered := make(chan struct{})
	release := make(chan struct{})
	accessDone := make(chan error, 1)
	go func() {
		accessDone <- ip.Access(idx, func(m *int) {
			close(entered)
			<-release
			*m = 7
		})
	}()
	<-entered

	begin := time.Now()
	e1 := ip.Access(idx, func(*int) {})
	e2 := ip.AccessLock(idx, func(*int) {})
	elapsed := time.Since(begin)
	r.Check(e1 == ErrInUseIndex && e2 == ErrInUseIndex && elapsed < holdTime/2,
		"Access 중 재진입", fmt.Sprintf("둘 다 ErrInUseIndex로 즉시 거부 (%v)", elapsed.Round(time.Millisecond)),
		fmt.Sprintf("access=%v accessLock=%v 소요=%v", e1, e2, elapsed))

	// 거부된 AccessLock이 Access의 사용 중 표시를 지우면
	// 사용 중인 슬롯이 Put으로 회수되어 콜백과 레이스가 발생한다.
	r.CheckErr(ip.Put(idx), ErrInUseIndex, "거부 후 Put 차단",
		"ErrInUseIndex (거부된 AccessLock이 상태를 훼손하지 않음)")

	close(release)
	r.CheckErr(<-accessDone, nil, "Access 완료", "성공")

	var kept int
	_ = ip.AccessLock(idx, func(m *int) { kept = *m })
	r.Check(kept == 7, "콜백 기록 보존", "Access 콜백이 쓴 값이 유지됨",
		fmt.Sprintf("value=%d", kept))

	// ── AccessLock 실행 중 → Access / AccessLock : 대기 후 실행
	ip2, _ := New[int](1)
	idx2, _ := ip2.Get()

	entered2 := make(chan struct{})
	go func() {
		_ = ip2.AccessLock(idx2, func(m *int) {
			close(entered2)
			time.Sleep(holdTime)
		})
	}()
	<-entered2

	begin = time.Now()
	e3 := ip2.Access(idx2, func(*int) {})
	waited := time.Since(begin)
	r.Check(e3 == nil && waited >= holdTime/2, "AccessLock 중 Access",
		fmt.Sprintf("거부되지 않고 %v 대기 후 실행", waited.Round(time.Millisecond)),
		fmt.Sprintf("err=%v 대기=%v", e3, waited))
}

// ---------------------------------------------------------------------------
// T7. 콜백 패닉 복구
// ---------------------------------------------------------------------------

func TestPool_PanicRecovery(t *testing.T) {
	r := testreport.New(t, "콜백 패닉 시 슬롯 락과 사용 중 표시가 복구되는지", "Capacity 1, 강제 패닉")
	defer r.Done()

	for _, tc := range []struct {
		name string
		call func(ip *Pool[int], idx int, fn func(*int)) error
	}{
		{"Access", func(ip *Pool[int], idx int, fn func(*int)) error { return ip.Access(idx, fn) }},
		{"AccessLock", func(ip *Pool[int], idx int, fn func(*int)) error { return ip.AccessLock(idx, fn) }},
	} {
		ip, _ := New[int](1)
		idx, _ := ip.Get()

		recovered := false
		func() {
			defer func() { recovered = recover() != nil }()
			_ = tc.call(ip, idx, func(*int) { panic("의도적 패닉") })
		}()
		r.Check(recovered, tc.name+" 패닉 전파", "호출 측에서 recover 가능", "패닉이 전파되지 않음")

		// 락이 남아 있으면 재진입에서 데드락에 빠진다.
		done := make(chan error, 1)
		go func() { done <- tc.call(ip, idx, func(m *int) { *m = 42 }) }()
		select {
		case err := <-done:
			r.Check(err == nil, tc.name+" 패닉 후 재진입", "데드락 없이 정상 실행",
				fmt.Sprintf("err=%v", err))
		case <-time.After(3 * time.Second):
			r.Check(false, tc.name+" 패닉 후 재진입", "", "데드락 (3초 내 반환되지 않음)")
		}

		// 사용 중 표시가 남아 있으면 반납이 거부된다.
		r.CheckErr(ip.Put(idx), nil, tc.name+" 패닉 후 반납", "사용 중 표시가 복구되어 반납 가능")
	}
}

// ---------------------------------------------------------------------------
// T8. Use 미들웨어 체인
// ---------------------------------------------------------------------------

func TestPool_Middleware(t *testing.T) {
	r := testreport.New(t, "Use 체인의 4단계 실행·접근자·중단 불가 설계 확인", "Capacity 2, 미들웨어 3개(nil 포함)")
	defer r.Done()

	ip, _ := New[int](2)

	var order []string
	ip.Use(func(c *Context[int]) {
		if c.Action() == ActionGet {
			order = append(order, "mw1-before")
		}
		c.Next()
		if c.Action() == ActionGet {
			order = append(order, "mw1-after")
		}
	})
	ip.Use(nil)
	// Next를 호출하지 않는 미들웨어. 체인은 중단되지 않아야 한다.
	ip.Use(func(c *Context[int]) {
		if c.Action() == ActionGet {
			order = append(order, "mw2-noNext")
		}
	})

	var acts []ActionType
	var lastIndex int
	var lastErr error
	ip.Use(func(c *Context[int]) {
		c.Next()
		acts = append(acts, c.Action())
		lastIndex, lastErr = c.Index(), c.Err()
	})

	idx, err := ip.Get()
	want := []string{"mw1-before", "mw2-noNext", "mw1-after"}
	r.Check(err == nil && equalStrings(order, want), "Get 단계 실행 순서",
		fmt.Sprintf("%v", want), fmt.Sprintf("%v (err=%v)", order, err))

	r.Check(err == nil, "nil 핸들러", "건너뛰고 정상 진행", fmt.Sprintf("err=%v", err))

	// Next를 호출하지 않는 미들웨어가 있어도 실제 할당이 이뤄져야 한다.
	r.Check(idx >= 0 && ip.Cap()-ip.Len() == 1, "Next 미호출 시 종단 실행",
		"중단되지 않고 실제 할당됨 (설계 확인)",
		fmt.Sprintf("idx=%d 사용 중=%d", idx, ip.Cap()-ip.Len()))

	r.Check(lastIndex == idx && lastErr == nil, "Get 단계 접근자",
		fmt.Sprintf("Index=%d Err=nil", idx),
		fmt.Sprintf("Index=%d Err=%v", lastIndex, lastErr))

	_ = ip.Access(idx, func(*int) {})
	_ = ip.AccessLock(idx, func(*int) {})
	_ = ip.Put(idx)

	wantActs := []ActionType{ActionGet, ActionAccess, ActionAccessLock, ActionPut}
	ok := len(acts) == len(wantActs)
	if ok {
		for i := range acts {
			if acts[i] != wantActs[i] {
				ok = false
				break
			}
		}
	}
	r.Check(ok, "4단계 모두 실행", "Get/Access/AccessLock/Put 순서대로 관찰됨",
		fmt.Sprintf("%v", acts))

	// 실패한 연산의 에러도 관찰되어야 한다.
	_ = ip.Put(idx)
	r.CheckErr(lastErr, ErrNotAllocatedIndex, "실패 연산의 Err 관찰",
		"중복 반납의 ErrNotAllocatedIndex가 관찰됨")
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// T9. 동시성 안전성 및 처리 성능
//
// 1단계는 Capacity 1로 단일 슬롯을 두고 경합시켜 상태 전이를 압박한다.
// 2단계는 고루틴마다 자기 슬롯을 확보할 수 있는 용량에서 처리량을 측정한다.
// 성능 수치는 머신 사양에 좌우되므로 단언하지 않고 참고로만 출력한다.
// ---------------------------------------------------------------------------

func TestPool_Concurrency(t *testing.T) {
	r := testreport.New(t,
		"멀티 고루틴 경합 안전성(1단계)과 처리 성능(2단계) 확인",
		"1단계 Capacity 1 / 2단계 Capacity 충분")
	defer r.Done()

	concurrencyStress(t, r)
	concurrencyPerf(t, r)
}

// 1단계: Capacity 1, 단일 슬롯 극한 경합. 안전성만 검증한다.
func concurrencyStress(t *testing.T, r *testreport.Report) {
	const workers = 100
	const loops = 300

	ip, _ := New[int](1)

	var getOK, getEmpty atomic.Uint64
	var accOK, accInUse, accNotAlloc atomic.Uint64
	var putOK, putInUse, putNotAlloc atomic.Uint64
	var unexpected atomic.Uint64

	var wg sync.WaitGroup
	start := make(chan struct{})
	begin := time.Now()

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < loops; j++ {
				idx, err := ip.Get()
				if err != nil {
					if err == ErrEmpty {
						getEmpty.Add(1)
					} else {
						unexpected.Add(1)
					}
					// 할당에 실패해도 다른 고루틴의 슬롯을 건드려 본다.
					switch e := ip.Access(0, func(m *int) { _ = *m }); e {
					case nil:
						accOK.Add(1)
					case ErrInUseIndex:
						accInUse.Add(1)
					case ErrNotAllocatedIndex:
						accNotAlloc.Add(1)
					default:
						unexpected.Add(1)
					}
					continue
				}
				getOK.Add(1)

				switch e := ip.Access(idx, func(m *int) { *m = j }); e {
				case nil:
					accOK.Add(1)
				case ErrInUseIndex:
					accInUse.Add(1)
				default:
					unexpected.Add(1)
				}

				// 다른 고루틴이 이 슬롯을 Access 중이면 반납이 거부된다.
				// 소유자는 성공할 때까지 재시도해야 슬롯이 누수되지 않는다.
				for {
					e := ip.Put(idx)
					if e == nil {
						putOK.Add(1)
						break
					}
					if e == ErrInUseIndex {
						putInUse.Add(1)
						runtime.Gosched()
						continue
					}
					if e == ErrNotAllocatedIndex {
						putNotAlloc.Add(1)
					} else {
						unexpected.Add(1)
					}
					break
				}
			}
		}()
	}

	close(start)
	finished := make(chan struct{})
	go func() { wg.Wait(); close(finished) }()

	select {
	case <-finished:
		r.Check(true, "1단계 완주",
			fmt.Sprintf("패닉·데드락 없이 %v 내 완료", time.Since(begin).Round(time.Millisecond)), "")
	case <-time.After(60 * time.Second):
		r.Check(false, "1단계 완주", "", "60초 내 완료되지 않음 (데드락 의심)")
		return
	}

	expected := uint64(workers * loops)
	r.Check(getOK.Load()+getEmpty.Load() == expected, "1단계 Get 정합성",
		fmt.Sprintf("Get %d건이 누락 없이 분류됨", expected),
		fmt.Sprintf("성공 %d + 고갈 %d (기대 %d)", getOK.Load(), getEmpty.Load(), expected))

	r.Check(unexpected.Load() == 0, "1단계 예상 외 에러",
		"없음 (Empty/InUse/NotAllocated만 발생)",
		fmt.Sprintf("%d건 발생", unexpected.Load()))

	// 재시도를 포함하면 할당한 만큼 정확히 반납되어야 한다.
	r.Check(putOK.Load() == getOK.Load(), "1단계 할당/반납 균형",
		fmt.Sprintf("할당 %d건이 재시도를 거쳐 전부 반납됨 (거부 %d회)", getOK.Load(), putInUse.Load()),
		fmt.Sprintf("할당 %d건, 반납 %d건", getOK.Load(), putOK.Load()))

	r.Check(ip.Len() == ip.Cap(), "1단계 종료 후 슬롯 회수", "모든 슬롯이 여유 상태로 복귀",
		fmt.Sprintf("Len=%d Cap=%d", ip.Len(), ip.Cap()))

	r.Note("1단계  Capacity 1, 고루틴 %d개, 총 %d회 Get, 소요 %v",
		workers, expected, time.Since(begin).Round(time.Millisecond))
	r.Note("       Get 성공 %d / 고갈 %d, Access 성공 %d / 사용중 %d / 미할당 %d",
		getOK.Load(), getEmpty.Load(), accOK.Load(), accInUse.Load(), accNotAlloc.Load())
	r.Note("       Put 성공 %d / 사용중 %d / 미할당 %d",
		putOK.Load(), putInUse.Load(), putNotAlloc.Load())
}

// 2단계: 고루틴마다 슬롯을 확보할 수 있는 용량에서 처리량을 측정한다.
func concurrencyPerf(t *testing.T, r *testreport.Report) {
	const workers = 8
	const loops = 20000
	const total = workers * loops

	measure := func(ip *Pool[int]) (time.Duration, uint64) {
		var failed atomic.Uint64
		var wg sync.WaitGroup
		start := make(chan struct{})
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				for j := 0; j < loops; j++ {
					idx, err := ip.Get()
					if err != nil {
						failed.Add(1)
						continue
					}
					if ip.Access(idx, func(m *int) { *m = j }) != nil {
						failed.Add(1)
					}
					if ip.Put(idx) != nil {
						failed.Add(1)
					}
				}
			}()
		}
		begin := time.Now()
		close(start)
		wg.Wait()
		return time.Since(begin), failed.Load()
	}

	// 워밍업: 첫 측정이 런타임/스케줄러 예열 비용을 떠안지 않도록 한다.
	warm, _ := New[int](workers * 4)
	_, _ = measure(warm)

	plain, _ := New[int](workers * 4)
	plainDur, plainFail := measure(plain)

	mw, _ := New[int](workers * 4)
	for i := 0; i < 3; i++ {
		mw.Use(func(c *Context[int]) { c.Next() })
	}
	mwDur, _ := measure(mw)

	r.Check(plainFail == 0 && plain.Len() == plain.Cap(), "2단계 성공 경로",
		fmt.Sprintf("%d회 Get+Access+Put 모두 성공, 슬롯 전량 회수", total),
		fmt.Sprintf("실패 %d건, Len=%d Cap=%d", plainFail, plain.Len(), plain.Cap()))

	perOp := func(d time.Duration) string {
		return fmt.Sprintf("평균 %v", (d / total).Round(time.Nanosecond))
	}
	throughput := func(d time.Duration) string {
		if d == 0 {
			return "-"
		}
		return fmt.Sprintf("%.2fM ops/s", float64(total)/d.Seconds()/1e6)
	}

	overhead := ""
	if plainDur > 0 {
		overhead = fmt.Sprintf(" (%+.1f%%)", (float64(mwDur)/float64(plainDur)-1)*100)
	}

	r.Note("2단계  Capacity %d, 고루틴 %d개, 총 %d회 사이클", workers*4, workers, total)
	r.Note("Get+Access+Put %-10s %s", perOp(plainDur), throughput(plainDur))
	r.Note("미들웨어 0개 → 3개  %v → %v%s",
		(plainDur / total).Round(time.Nanosecond), (mwDur / total).Round(time.Nanosecond), overhead)
	r.Note("(-race 실행 시 위 수치는 수 배 부풀려짐)")
}
