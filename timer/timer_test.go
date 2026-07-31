package timer

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	"time"

	"github.com/team-core-1/util/internal/testreport"

	"github.com/RussellLuo/timingwheel"
)

func TestMain(m *testing.M) { testreport.Main(m, "timer") }

// ---------------------------------------------------------------------------
// 공통 도우미
// ---------------------------------------------------------------------------

const (
	tick      = 10 * time.Millisecond
	wheelSize = 20
	// 만료를 기다릴 때 쓰는 여유 시간. 부하가 있어도 흔들리지 않도록 넉넉히 둔다.
	waitTimeout = 5 * time.Second
	// 시험 중 만료되면 안 되는 타이머에 쓰는 시간
	neverFire = 1 * time.Hour
)

func newWheel(t *testing.T) *timingwheel.TimingWheel {
	tw := timingwheel.NewTimingWheel(tick, wheelSize)
	tw.Start()
	t.Cleanup(tw.Stop)
	return tw
}

// drain은 C()를 계속 수신하며 수신 건수를 센다. 반환된 함수는 종료를 기다린다.
func drain[T any](eng *Engine[T], counter *atomic.Uint64) func() {
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range eng.C() {
			if counter != nil {
				counter.Add(1)
			}
		}
	}()
	return func() { <-done }
}

// ---------------------------------------------------------------------------
// T1. 생성과 소멸
// ---------------------------------------------------------------------------

func TestTimer_NewAndClose(t *testing.T) {
	r := testreport.New(t, "New의 인자 검증과 Close 이후 상태 전이 확인", "tick 10ms, wheelSize 20, Capacity 10")
	defer r.Done()

	_, err := New[int](nil, 10)
	r.CheckErr(err, ErrNilTimingWheel, "New(nil 휠)", "ErrNilTimingWheel")

	tw := newWheel(t)

	_, err = New[int](tw, 0)
	r.CheckErr(err, ErrInvalidCap, "New(cap 0)", "ErrInvalidCap")

	_, err = New[int](tw, -1)
	r.CheckErr(err, ErrInvalidCap, "New(cap -1)", "ErrInvalidCap")

	eng, err := New[int](tw, 10)
	if err != nil {
		t.Fatalf("New 실패: %v", err)
	}
	r.Check(eng.Len() == 0 && eng.Cap() == 10 && !eng.IsClosed() && eng.QFail() == 0,
		"생성 직후 상태", "Len=0 Cap=10 IsClosed=false QFail=0",
		fmt.Sprintf("Len=%d Cap=%d IsClosed=%v QFail=%d", eng.Len(), eng.Cap(), eng.IsClosed(), eng.QFail()))

	eng.Close()
	r.Check(eng.IsClosed(), "Close 후 IsClosed", "true", "false")

	safe := func() (ok bool) {
		defer func() { ok = recover() == nil }()
		eng.Close()
		eng.Use(func(c *Context[int]) { c.Next() })
		return
	}()
	r.Check(safe, "Close 후 중복 호출", "Close/Use 모두 패닉 없음", "패닉 발생")
}

// ---------------------------------------------------------------------------
// T2. 기본 연산 정상 경로
// ---------------------------------------------------------------------------

func TestTimer_BasicOps(t *testing.T) {
	r := testreport.New(t, "Set한 타이머가 만료되어 C()로 전달되고 Cancel이 이를 막는지", "Capacity 10, 소비자 1개")
	defer r.Done()

	tw := newWheel(t)
	eng, _ := New[int](tw, 10)
	defer eng.Close()

	tm, err := eng.Set(2*tick, 100)
	// *Timer를 %v로 출력하면 리플렉션이 내부 필드를 읽어 만료 고루틴과 경합한다.
	r.Check(err == nil && tm != nil, "Set 반환", "nil 에러와 유효한 Timer",
		fmt.Sprintf("err=%v timer!=nil=%v", err, tm != nil))

	select {
	case key := <-eng.C():
		r.Check(key == 100, "만료 키 전달", "Set한 키 100이 그대로 전달됨",
			fmt.Sprintf("수신 키=%d", key))
	case <-time.After(waitTimeout):
		r.Check(false, "만료 키 전달", "", "제한 시간 내 수신되지 않음")
	}

	// 취소한 타이머는 만료되지 않아야 한다.
	tm2, _ := eng.Set(2*tick, 200)
	r.CheckErr(eng.Cancel(tm2), nil, "Cancel 반환", "성공")

	select {
	case key := <-eng.C():
		r.Check(false, "취소 후 미전달", "", fmt.Sprintf("취소했는데 키 %d가 전달됨", key))
	case <-time.After(10 * tick):
		r.Check(true, "취소 후 미전달", "만료 시간이 지나도 전달되지 않음", "")
	}

	r.Check(eng.Len() == 0, "정리 후 잔여", "Len=0", fmt.Sprintf("Len=%d", eng.Len()))
}

// ---------------------------------------------------------------------------
// T3. 에러 경로 전수
// ---------------------------------------------------------------------------

func TestTimer_Errors(t *testing.T) {
	r := testreport.New(t, "정의된 에러가 정확한 조건에서 반환되는지", "Capacity 2, 만료되지 않는 타이머 사용")
	defer r.Done()

	tw := newWheel(t)
	eng, _ := New[int](tw, 2)

	var nilEng *Engine[int]
	_, err := nilEng.Set(neverFire, 1)
	r.CheckErr(err, ErrNil, "nil 엔진 Set", "ErrNil")
	r.CheckErr(nilEng.Cancel(&Timer{}), ErrNil, "nil 엔진 Cancel", "ErrNil")
	r.CheckErr(eng.Cancel(nil), ErrNilTimer, "nil Timer Cancel", "ErrNilTimer")

	tm1, e1 := eng.Set(neverFire, 1)
	_, e2 := eng.Set(neverFire, 2)
	_, e3 := eng.Set(neverFire, 3)
	r.Check(e1 == nil && e2 == nil && e3 == ErrExpiredQueueFull, "정원 초과 Set",
		"정원까지 성공, 초과분은 ErrExpiredQueueFull",
		fmt.Sprintf("1=%v 2=%v 3=%v", e1, e2, e3))

	r.CheckErr(eng.Cancel(tm1), nil, "정상 Cancel", "성공")
	r.CheckErr(eng.Cancel(tm1), ErrAlreadyCancelled, "중복 Cancel", "ErrAlreadyCancelled")

	// 엔진이 발급하지 않은 Timer는 소유권 검사에서 걸러진다.
	r.CheckErr(eng.Cancel(&Timer{}), ErrNotOwner, "직접 생성한 Timer Cancel", "ErrNotOwner")

	eng.Close()
	_, err = eng.Set(neverFire, 9)
	r.CheckErr(err, ErrClosed, "Close 후 Set", "ErrClosed")
}

// ---------------------------------------------------------------------------
// T4. nil 리시버 안전성
// ---------------------------------------------------------------------------

func TestTimer_NilReceiver(t *testing.T) {
	r := testreport.New(t, "nil 엔진에 대한 모든 공개 메서드가 패닉 없이 방어되는지", "초기화하지 않은 *Engine 포인터")
	defer r.Done()

	var eng *Engine[int]

	tm, err := eng.Set(neverFire, 1)
	r.Check(err == ErrNil && tm == nil, "Set", "ErrNil, nil Timer 반환",
		fmt.Sprintf("err=%v timer==nil=%v", err, tm == nil))

	r.CheckErr(eng.Cancel(&Timer{}), ErrNil, "Cancel", "ErrNil")
	r.Check(eng.C() == nil, "C", "nil 채널 반환", "nil이 아닌 채널 반환")
	r.Check(eng.Len() == 0 && eng.Cap() == 0 && eng.QFail() == 0, "Len/Cap/QFail", "모두 0",
		fmt.Sprintf("Len=%d Cap=%d QFail=%d", eng.Len(), eng.Cap(), eng.QFail()))
	r.Check(eng.IsClosed(), "IsClosed", "true", "false")

	safe := func() (ok bool) {
		defer func() { ok = recover() == nil }()
		eng.Use(func(c *Context[int]) {})
		eng.Close()
		return
	}()
	r.Check(safe, "Use/Close", "패닉 없음", "패닉 발생")
}

// ---------------------------------------------------------------------------
// T5. 정원과 카운터
// ---------------------------------------------------------------------------

func TestTimer_LenCap(t *testing.T) {
	r := testreport.New(t, "Set/Cancel/만료에 따른 Len 변화와 정원 관리 확인", "Capacity 3, 만료되지 않는 타이머 사용")
	defer r.Done()

	tw := newWheel(t)
	eng, _ := New[int](tw, 3)
	defer eng.Close()

	r.Check(eng.Len() == 0 && eng.Cap() == 3, "초기 상태", "Len=0 Cap=3",
		fmt.Sprintf("Len=%d Cap=%d", eng.Len(), eng.Cap()))

	timers := make([]*Timer, 0, 3)
	for i := 0; i < 3; i++ {
		tm, err := eng.Set(neverFire, i)
		if err != nil {
			t.Fatalf("Set(%d) 실패: %v", i, err)
		}
		timers = append(timers, tm)
	}
	r.Check(eng.Len() == 3, "정원까지 Set", "Len=3", fmt.Sprintf("Len=%d", eng.Len()))

	_, err := eng.Set(neverFire, 99)
	r.CheckErr(err, ErrExpiredQueueFull, "정원 도달 후 Set", "ErrExpiredQueueFull")

	_ = eng.Cancel(timers[0])
	r.Check(eng.Len() == 2, "Cancel 후", "Len=2", fmt.Sprintf("Len=%d", eng.Len()))

	// 취소로 확보된 자리를 다시 쓸 수 있어야 한다.
	tm, err := eng.Set(neverFire, 100)
	r.Check(err == nil && eng.Len() == 3, "취소 자리 재사용", "Set 성공, Len=3",
		fmt.Sprintf("err=%v Len=%d", err, eng.Len()))
	if tm != nil {
		_ = eng.Cancel(tm)
	}

	// 만료된 타이머도 전달 후에는 자리를 반납해야 한다.
	for _, x := range timers[1:] {
		_ = eng.Cancel(x)
	}
	var received atomic.Uint64
	stop := drain(eng, &received)
	if _, err := eng.Set(2*tick, 7); err != nil {
		t.Fatalf("만료용 Set 실패: %v", err)
	}
	settled := false
	for i := 0; i < 200; i++ {
		if received.Load() == 1 && eng.Len() == 0 {
			settled = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	r.Check(settled, "만료 후 자리 반납", "전달 완료 후 Len=0으로 복귀",
		fmt.Sprintf("수신 %d건, Len=%d", received.Load(), eng.Len()))
	eng.Close()
	stop()
}

// ---------------------------------------------------------------------------
// T6. Cancel 소유권
//
// 소유권 검사가 없으면 취소를 요청한 엔진의 카운터가 부당하게 감소해 음수가 되고,
// 실제 소유 엔진은 카운터가 줄지 않아 정원을 영구히 잠식당한다.
// ---------------------------------------------------------------------------

func TestTimer_CancelOwnership(t *testing.T) {
	r := testreport.New(t, "타 엔진이 발급한 Timer의 Cancel 차단과 카운터 무결성 확인", "같은 timingWheel을 공유하는 엔진 2개, Capacity 2")
	defer r.Done()

	tw := newWheel(t)
	engA, _ := New[int](tw, 2)
	engB, _ := New[int](tw, 2)
	defer engA.Close()
	defer engB.Close()

	tmB, err := engB.Set(neverFire, 100)
	if err != nil {
		t.Fatalf("B.Set 실패: %v", err)
	}

	r.CheckErr(engA.Cancel(tmB), ErrNotOwner, "타 엔진 Timer Cancel", "ErrNotOwner")
	r.Check(engA.Len() == 0, "거부 후 요청 측 카운터", "Len=0 (음수 아님)",
		fmt.Sprintf("Len=%d", engA.Len()))
	r.Check(engB.Len() == 1, "거부 후 소유 측 카운터", "Len=1 (취소되지 않음)",
		fmt.Sprintf("Len=%d", engB.Len()))

	// 타입이 다른 엔진도 차단되어야 한다.
	engStr, _ := New[string](tw, 2)
	defer engStr.Close()
	r.CheckErr(engStr.Cancel(tmB), ErrNotOwner, "타입이 다른 엔진 Cancel", "ErrNotOwner")

	// 소유권 검사가 중복 취소 검사보다 먼저 수행된다.
	r.CheckErr(engB.Cancel(tmB), nil, "소유 엔진 Cancel", "성공")
	r.CheckErr(engA.Cancel(tmB), ErrNotOwner, "이미 취소된 타 엔진 Timer",
		"ErrNotOwner (소유권 검사 우선)")

	// 취소로 반납된 자리를 다시 쓸 수 있어야 한다 (정원 잠식 없음)
	reuse := true
	for i := 0; i < 2; i++ {
		if _, err := engB.Set(neverFire, i); err != nil {
			reuse = false
		}
	}
	r.Check(reuse && engB.Len() == 2, "취소 후 정원 재사용", "Capacity 2를 모두 다시 사용 가능",
		fmt.Sprintf("재사용=%v Len=%d", reuse, engB.Len()))
}

// ---------------------------------------------------------------------------
// T7. Use 미들웨어 체인
// ---------------------------------------------------------------------------

func TestTimer_Middleware(t *testing.T) {
	r := testreport.New(t, "Use 체인의 단계별 실행·접근자·중단 불가 설계 확인", "Capacity 10, 미들웨어 3개(nil 포함)")
	defer r.Done()

	tw := newWheel(t)
	eng, _ := New[int](tw, 10)
	defer eng.Close()

	var setCnt, cancelCnt, timeoutCnt atomic.Uint64
	var order []string
	var orderMu sync.Mutex

	eng.Use(func(c *Context[int]) {
		if c.Action() == ActionSet {
			orderMu.Lock()
			order = append(order, "mw1-before")
			orderMu.Unlock()
		}
		c.Next()
		if c.Action() == ActionSet {
			orderMu.Lock()
			order = append(order, "mw1-after")
			orderMu.Unlock()
		}
	})
	eng.Use(nil)
	// Next를 호출하지 않는 미들웨어. 체인은 중단되지 않아야 한다.
	eng.Use(func(c *Context[int]) {
		if c.Action() == ActionSet {
			orderMu.Lock()
			order = append(order, "mw2-noNext")
			orderMu.Unlock()
		}
	})

	// Set 단계 접근자는 뒤 연산에 덮이므로 Set 직후에 확인한다.
	// Set/Cancel 단계는 호출 고루틴에서 동기 실행되므로 아래 변수는
	// 테스트 고루틴에서만 접근된다. Timeout 단계는 별도 고루틴이라 atomic을 쓴다.
	var setKey int
	var setAct ActionType
	var setErr error
	var setErrSeen bool
	var cancelErr error
	var cancelErrSeen bool
	var timeoutErrNil atomic.Bool
	eng.Use(func(c *Context[int]) {
		switch c.Action() {
		case ActionSet:
			setCnt.Add(1)
			setAct, setKey = c.Action(), c.Key()
			c.Next()
			setErr, setErrSeen = c.Err(), true
		case ActionCancel:
			cancelCnt.Add(1)
			c.Next()
			cancelErr, cancelErrSeen = c.Err(), true
		case ActionTimeout:
			timeoutCnt.Add(1)
			c.Next()
			timeoutErrNil.Store(c.Err() == nil)
		default:
			c.Next()
		}
	})

	tm, err := eng.Set(2*tick, 42)
	r.Check(setAct == ActionSet && setKey == 42, "Set 단계 접근자", "Action=Set Key=42",
		fmt.Sprintf("Action=%v Key=%d", setAct, setKey))

	orderMu.Lock()
	got := append([]string(nil), order...)
	orderMu.Unlock()
	want := []string{"mw1-before", "mw2-noNext", "mw1-after"}
	r.Check(err == nil && equalStrings(got, want), "Set 단계 실행 순서",
		fmt.Sprintf("%v", want), fmt.Sprintf("%v (err=%v)", got, err))

	r.Check(err == nil && tm != nil, "nil 핸들러", "건너뛰고 정상 진행",
		fmt.Sprintf("err=%v timer!=nil=%v", err, tm != nil))

	// Next를 호출하지 않는 미들웨어가 있어도 실제 등록·만료가 이뤄져야 한다.
	select {
	case key := <-eng.C():
		r.Check(key == 42, "Next 미호출 시 종단 실행",
			"중단되지 않고 실제 만료·전달됨 (설계 확인)", fmt.Sprintf("수신 키=%d", key))
	case <-time.After(waitTimeout):
		r.Check(false, "Next 미호출 시 종단 실행", "", "제한 시간 내 전달되지 않음")
	}

	tm2, _ := eng.Set(neverFire, 7)
	_ = eng.Cancel(tm2)

	r.Check(setCnt.Load() == 2 && cancelCnt.Load() == 1 && timeoutCnt.Load() == 1,
		"3단계 모두 실행", "Set 2회 / Cancel 1회 / Timeout 1회",
		fmt.Sprintf("Set %d / Cancel %d / Timeout %d", setCnt.Load(), cancelCnt.Load(), timeoutCnt.Load()))

	// Next 이후 각 단계의 결과를 Context.Err()로 관찰할 수 있어야 한다.
	r.Check(setErrSeen && setErr == nil && cancelErrSeen && cancelErr == nil && timeoutErrNil.Load(),
		"Next 이후 Err 관찰", "Set/Cancel/Timeout 모두 성공이 nil로 관찰됨",
		fmt.Sprintf("set=%v(%v) cancel=%v(%v) timeout=%v",
			setErrSeen, setErr, cancelErrSeen, cancelErr, timeoutErrNil.Load()))

	// 실패한 연산의 에러도 관찰되어야 한다.
	_ = eng.Cancel(tm2)
	r.Check(cancelErrSeen && cancelErr == ErrAlreadyCancelled, "실패 연산의 Err 관찰",
		"중복 Cancel의 ErrAlreadyCancelled가 관찰됨",
		fmt.Sprintf("Err=%v", cancelErr))
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
// T8. Close 의미론
//
// Close는 이미 Set된 타이머를 취소하지 않는다. 엔진이 발급한 *Timer 핸들을
// 보관하지 않으므로 일괄 취소를 지원할 수 없기 때문이다. 만료된 키는 닫힌 큐로
// 유입되어 전달되지 못하고 QFail로만 집계된다.
// ---------------------------------------------------------------------------

func TestTimer_CloseSemantics(t *testing.T) {
	r := testreport.New(t, "Close 이후 Set 차단과 대기 타이머 처리, 자원 정리 확인", "Capacity 10")
	defer r.Done()

	tw := newWheel(t)

	runtime.GC()
	base := runtime.NumGoroutine()

	eng, _ := New[int](tw, 10)
	r.Check(runtime.NumGoroutine() > base, "New의 내부 고루틴", "생성 시 만료 큐 고루틴 기동",
		fmt.Sprintf("고루틴 수 변화 없음 (%d)", base))

	// Close 이후 Set은 카운터를 소모하지 않고 거부되어야 한다.
	tm, _ := eng.Set(neverFire, 1)
	_ = eng.Cancel(tm)
	before := eng.Len()

	// 곧 만료될 타이머를 남겨 둔 채 Close한다.
	pending := 3
	for i := 0; i < pending; i++ {
		if _, err := eng.Set(2*tick, i); err != nil {
			t.Fatalf("Set 실패: %v", err)
		}
	}
	eng.Close()

	rejected := 0
	for i := 0; i < 5; i++ {
		tmX, err := eng.Set(neverFire, i)
		if err == ErrClosed && tmX == nil {
			rejected++
		}
	}
	r.Check(rejected == 5, "Close 후 Set 차단", "5회 모두 ErrClosed와 nil Timer",
		fmt.Sprintf("%d회만 차단됨", rejected))
	r.Check(eng.Len() == before+pending, "거부된 Set의 카운터", "정원을 소모하지 않음",
		fmt.Sprintf("Len=%d (기대 %d)", eng.Len(), before+pending))

	// 대기 중이던 타이머는 취소되지 않고 만료되어 QFail로 집계된다.
	settled := false
	for i := 0; i < 300; i++ {
		if eng.QFail() >= pending {
			settled = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	r.Check(settled, "대기 타이머 만료 처리",
		fmt.Sprintf("Close 후에도 만료되어 QFail %d건으로 집계됨 (의도된 동작)", eng.QFail()),
		fmt.Sprintf("QFail=%d (기대 %d 이상)", eng.QFail(), pending))

	// 만료 큐가 닫히면 내부 고루틴이 종료된다.
	for range eng.C() {
	}
	recovered := false
	for i := 0; i < 200; i++ {
		if runtime.NumGoroutine() <= base {
			recovered = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	r.Check(recovered, "Close 후 고루틴 회수", fmt.Sprintf("기준선 %d개로 복귀", base),
		fmt.Sprintf("기준선 %d개, 현재 %d개", base, runtime.NumGoroutine()))
}

// ---------------------------------------------------------------------------
// T9. 동시성 안전성 및 처리 성능
//
// 1단계는 Capacity 1로 정원 경계를 압박하고 경합 중 Close를 끼워 넣어
// 등록 되돌리기까지 포함한 카운터 무결성을 본다.
// 2단계는 정원이 넉넉한 조건에서 Set/Cancel 처리량을 측정한다.
// 성능 수치는 머신 사양에 좌우되므로 단언하지 않고 참고로만 출력한다.
// ---------------------------------------------------------------------------

func TestTimer_Concurrency(t *testing.T) {
	r := testreport.New(t,
		"멀티 고루틴 경합 안전성(1단계)과 처리 성능(2단계) 확인",
		"1단계 Capacity 1 + 중간 Close / 2단계 Capacity 충분")
	defer r.Done()

	concurrencyStress(t, r)
	concurrencyPerf(t, r)
}

// 1단계: Capacity 1, 극한 경합 + 경합 중 Close. 안전성만 검증한다.
//
// Close 경합 구간(등록 직후 재확인 후 되돌리기)은 결정적으로 만들 수 없으므로,
// 짧은 사이클을 여러 번 반복해 노출 확률을 높인다.
func concurrencyStress(t *testing.T, r *testreport.Report) {
	const cycles = 30
	for i := 0; i < cycles; i++ {
		closeRaceCycle(t)
	}
	for i := 0; i < 5; i++ {
		cancelRaceCycle(t)
	}

	const workers = 50
	const loops = 200

	tw := newWheel(t)
	eng, _ := New[int](tw, 1)

	var attempts atomic.Uint64
	var setOK, setFull, setClosed atomic.Uint64
	var cancelOK, cancelAlready, cancelOther atomic.Uint64
	var unexpected atomic.Uint64
	var received atomic.Uint64

	stop := drain(eng, &received)

	var minLen, maxLen atomic.Int64
	maxLen.Store(-1 << 62)
	observe := func() {
		l := int64(eng.Len())
		for {
			m := minLen.Load()
			if l >= m || minLen.CompareAndSwap(m, l) {
				break
			}
		}
		for {
			m := maxLen.Load()
			if l <= m || maxLen.CompareAndSwap(m, l) {
				break
			}
		}
	}

	var wg sync.WaitGroup
	start := make(chan struct{})
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			<-start
			for j := 0; j < loops; j++ {
				attempts.Add(1)
				tm, err := eng.Set(2*tick, w*loops+j)
				observe()
				switch err {
				case nil:
					setOK.Add(1)
					if j%2 == 0 {
						switch cErr := eng.Cancel(tm); cErr {
						case nil:
							cancelOK.Add(1)
						case ErrAlreadyCancelled:
							cancelAlready.Add(1)
						default:
							cancelOther.Add(1)
						}
					}
				case ErrExpiredQueueFull:
					setFull.Add(1)
				case ErrClosed:
					setClosed.Add(1)
				default:
					unexpected.Add(1)
				}
			}
		}(w)
	}

	// Close 시점을 고정 시간이 아니라 진행률로 잡는다.
	// 시간으로 잡으면 머신 속도에 따라 작업이 먼저 끝나 버려
	// Close 이후 시도가 하나도 없는 경우가 생긴다.
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		target := uint64(workers*loops) * 3 / 10
		for attempts.Load() < target {
			runtime.Gosched()
		}
		eng.Close()
	}()

	begin := time.Now()
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
	stop()

	expected := uint64(workers * loops)
	total := setOK.Load() + setFull.Load() + setClosed.Load()
	r.Check(total == expected, "1단계 시도 횟수 정합성",
		fmt.Sprintf("Set %d건이 누락 없이 분류됨", expected),
		fmt.Sprintf("분류 합계 %d (기대 %d)", total, expected))

	r.Check(unexpected.Load() == 0 && cancelOther.Load() == 0, "1단계 예상 외 에러",
		"없음 (Full/Closed/AlreadyCancelled만 발생)",
		fmt.Sprintf("Set %d건, Cancel %d건", unexpected.Load(), cancelOther.Load()))

	// 하한만 단언한다. 음수는 카운터 증감이 어긋났다는 뜻이라 결함이다.
	//
	// 상한은 단언하지 않는다. Len은 active와 만료 큐 적재량을 각각 읽어 더하므로
	// 원자적 스냅숏이 아니고, 만료 처리 중인 타이머는 큐에 넣은 뒤 active가
	// 감소하기 전까지 양쪽에 이중 계상되어 일시적으로 Cap을 넘을 수 있다.
	r.Check(minLen.Load() >= 0, "1단계 카운터 하한", "Len이 음수로 관측되지 않음",
		fmt.Sprintf("최소 Len=%d", minLen.Load()))

	// Close가 반영되었다는 사실만 단언한다.
	// "경합 중 몇 건이 ErrClosed로 거부되었는가"는 Close 완료 시점과 작업 종료 시점의
	// 경합에 좌우되어 0건일 수도 있으므로 단언하지 않고 측정값으로만 남긴다.
	// Close 이후 Set이 차단되는지는 TestTimer_CloseSemantics에서 결정적으로 확인한다.
	r.Check(eng.IsClosed(), "1단계 Close 반영", "경합 중 호출한 Close가 반영됨", "IsClosed=false")

	// 남은 타이머가 모두 만료될 때까지 기다린 뒤 카운터 수렴을 본다.
	converged := false
	for i := 0; i < 300; i++ {
		if eng.Len() == 0 {
			converged = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	r.Check(converged, "1단계 카운터 수렴", "모든 타이머 처리 후 Len=0",
		fmt.Sprintf("Len=%d QFail=%d", eng.Len(), eng.QFail()))

	r.Note("Len 관측 범위 [%d, %d] (Cap %d, 만료 중 이중 계상으로 상한 초과 가능)",
		minLen.Load(), maxLen.Load(), eng.Cap())
	r.Note("1단계  Capacity 1, 고루틴 %d개, 총 %d회 Set, 소요 %v",
		workers, expected, time.Since(begin).Round(time.Millisecond))
	r.Note("       성공 %d / 정원초과 %d / 종료거부 %d / 취소 %d / 수신 %d",
		setOK.Load(), setFull.Load(), setClosed.Load(), cancelOK.Load(), received.Load())
}

// closeRaceCycle은 Set이 진행되는 도중 Close를 걸어
// "검사 통과 후 등록 직전에 Close가 완료되는" 경합 구간을 노출시킨다.
func closeRaceCycle(t *testing.T) {
	tw := newWheel(t)
	eng, _ := New[int](tw, 1000)
	stop := drain(eng, nil)

	var wg sync.WaitGroup
	start := make(chan struct{})
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for {
				select {
				case <-start:
				default:
				}
				tm, err := eng.Set(neverFire, 1)
				if err == ErrClosed {
					return
				}
				if err == nil {
					_ = eng.Cancel(tm)
				}
			}
		}()
	}
	close(start)
	time.Sleep(200 * time.Microsecond)
	eng.Close()
	wg.Wait()
	stop()
}

// cancelRaceCycle은 즉시 만료되는 타이머를 Cancel과 정면으로 경합시켜
// "Cancel이 먼저 처리되어 만료 콜백이 취소로 판정하는" 구간을 노출시킨다.
// 이 판정이 어긋나면 active가 이중 감소해 카운터가 음수가 된다.
func cancelRaceCycle(t *testing.T) {
	const n = 500

	tw := newWheel(t)
	eng, _ := New[int](tw, n+1)
	stop := drain(eng, nil)

	var cancelWon, timeoutWon, other int
	for i := 0; i < n; i++ {
		// d=0이면 timingWheel이 즉시 별도 고루틴에서 만료 콜백을 실행한다.
		// 그 콜백과 아래 Cancel이 timer.mu를 두고 경합한다.
		tm, err := eng.Set(0, i)
		if err != nil {
			continue
		}
		switch eng.Cancel(tm) {
		case nil:
			cancelWon++
		case ErrAlreadyCancelled:
			timeoutWon++
		default:
			other++
		}
	}

	if other != 0 {
		t.Errorf("Cancel/만료 경합에서 예상 외 에러 %d건", other)
	}

	// 카운터가 0으로 수렴해야 한다. 이중 감소가 있으면 음수로 남는다.
	for i := 0; i < 200; i++ {
		if eng.Len() == 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if l := eng.Len(); l != 0 {
		t.Errorf("Cancel/만료 경합 후 카운터 미수렴: Len=%d (Cancel승 %d, 만료승 %d)",
			l, cancelWon, timeoutWon)
	}

	eng.Close()
	stop()
}

// 2단계: 정원이 넉넉한 조건에서 Set/Cancel 처리량을 측정한다.
func concurrencyPerf(t *testing.T, r *testreport.Report) {
	const workers = 8
	const loops = 3000
	const total = workers * loops

	measure := func(eng *Engine[int]) (time.Duration, uint64) {
		var failed atomic.Uint64
		var wg sync.WaitGroup
		start := make(chan struct{})
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
				<-start
				for j := 0; j < loops; j++ {
					tm, err := eng.Set(neverFire, w*loops+j)
					if err != nil {
						failed.Add(1)
						continue
					}
					if eng.Cancel(tm) != nil {
						failed.Add(1)
					}
				}
			}(w)
		}
		begin := time.Now()
		close(start)
		wg.Wait()
		return time.Since(begin), failed.Load()
	}

	tw := newWheel(t)

	// 워밍업: 첫 측정이 런타임/스케줄러 예열 비용을 떠안지 않도록 한다.
	warm, _ := New[int](tw, total+1)
	_, _ = measure(warm)
	warm.Close()

	plain, _ := New[int](tw, total+1)
	stopPlain := drain(plain, nil)
	plainDur, plainFail := measure(plain)

	mw, _ := New[int](tw, total+1)
	for i := 0; i < 3; i++ {
		mw.Use(func(c *Context[int]) { c.Next() })
	}
	stopMW := drain(mw, nil)
	mwDur, _ := measure(mw)

	r.Check(plainFail == 0 && plain.Len() == 0, "2단계 성공 경로",
		fmt.Sprintf("%d회 Set+Cancel 모두 성공, 카운터 0 복귀", total),
		fmt.Sprintf("실패 %d건, Len=%d", plainFail, plain.Len()))

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

	r.Note("2단계  Capacity %d, 고루틴 %d개, 총 %d회 Set+Cancel", total+1, workers, total)
	r.Note("Set+Cancel %-14s %s", perOp(plainDur), throughput(plainDur))
	r.Note("미들웨어 0개 → 3개  %v → %v%s",
		(plainDur / total).Round(time.Nanosecond), (mwDur / total).Round(time.Nanosecond), overhead)
	r.Note("(-race 실행 시 위 수치는 수 배 부풀려짐)")

	plain.Close()
	stopPlain()
	mw.Close()
	stopMW()
}
