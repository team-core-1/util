package pipequeue

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	"time"

	"github.com/team-core-1/util/internal/testreport"
)

func TestMain(m *testing.M) { testreport.Main(m, "pipequeue") }

// enterPipe는 pipe 고루틴이 항목을 꺼내 파이프 단계에 진입한 시점을 알리는
// 채널을 등록한다. 수신자를 두지 않으면 그 항목은 in-flight 상태로 머무른다.
// 고정 대기(sleep) 대신 이 신호를 쓰면 타이밍에 의존하지 않는다.
func enterPipe[T any](q *Queue[T]) <-chan struct{} {
	entered := make(chan struct{})
	var once sync.Once
	q.Use(func(c *Context[T]) {
		if c.Action() == ActionPipe {
			once.Do(func() { close(entered) })
		}
		c.Next()
	})
	return entered
}

func waitSignal(r *testreport.Report, ch <-chan struct{}, name string) bool {
	select {
	case <-ch:
		return true
	case <-time.After(3 * time.Second):
		r.Check(false, name, "", "3초 내 pipe 단계 진입 신호 없음")
		return false
	}
}

// ---------------------------------------------------------------------------
// T1. 생성과 소멸
// ---------------------------------------------------------------------------

func TestQueue_NewAndClose(t *testing.T) {
	r := testreport.New(t, "New의 용량 검증과 Close 이후 상태 전이 확인", "Capacity 4, 단일 고루틴")
	defer r.Done()

	_, err := New[int](0)
	r.CheckErr(err, ErrInvalidCap, "New(0)", "ErrInvalidCap")

	_, err = New[int](-1)
	r.CheckErr(err, ErrInvalidCap, "New(-1)", "ErrInvalidCap")

	q, err := New[int](4)
	if err != nil {
		t.Fatalf("New(4) 실패: %v", err)
	}
	r.Check(q.Len() == 0 && q.Cap() == 4 && !q.IsFull() && !q.IsClosed(),
		"생성 직후 상태", "Len=0 Cap=4 IsFull=false IsClosed=false",
		fmt.Sprintf("Len=%d Cap=%d IsFull=%v IsClosed=%v", q.Len(), q.Cap(), q.IsFull(), q.IsClosed()))

	q.Close()
	r.Check(q.IsClosed(), "Close 후 IsClosed", "true", "false")

	safe := func() (ok bool) {
		defer func() { ok = recover() == nil }()
		q.Close()
		q.Use(func(c *Context[int]) { c.Next() })
		return
	}()
	r.Check(safe, "Close 후 중복 호출", "Close/Use 모두 패닉 없음", "패닉 발생")
}

// ---------------------------------------------------------------------------
// T2. 기본 연산 정상 경로
// ---------------------------------------------------------------------------

func TestQueue_BasicOps(t *testing.T) {
	r := testreport.New(t, "Put한 데이터가 C()로 순서대로 전달되는지 확인", "Capacity 8, 소비자 1개")
	defer r.Done()

	q, _ := New[int](8)
	defer q.Close()

	const n = 8
	received := make([]int, 0, n)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < n; i++ {
			received = append(received, <-q.C())
		}
	}()

	putErr := 0
	for i := 0; i < n; i++ {
		if q.Put(i) != nil {
			putErr++
		}
	}

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		r.Check(false, "전량 수신", "", "3초 내 수신 완료되지 않음")
		return
	}

	ordered := true
	for i, v := range received {
		if v != i {
			ordered = false
			break
		}
	}
	r.Check(putErr == 0, "전량 Put", fmt.Sprintf("%d건 모두 성공", n), fmt.Sprintf("%d건 실패", putErr))
	r.Check(len(received) == n, "전량 수신", fmt.Sprintf("%d건 수신", n), fmt.Sprintf("%d건만 수신", len(received)))
	r.Check(ordered, "순서 보존", "투입 순서대로 전달됨", fmt.Sprintf("순서 어긋남: %v", received))

	// 여러 큐 인스턴스가 서로 간섭하지 않아야 한다.
	independent := true
	for i := 0; i < 50; i++ {
		iq, _ := New[int](1)
		want := i + 5000
		if iq.Put(want) != nil || <-iq.C() != want {
			independent = false
		}
		iq.Close()
	}
	r.Check(independent, "인스턴스 독립성", "50개 큐가 각자 값을 정확히 전달", "인스턴스 간 값 혼선 발생")
}

// ---------------------------------------------------------------------------
// T3. 에러 경로 전수
// ---------------------------------------------------------------------------

func TestQueue_Errors(t *testing.T) {
	r := testreport.New(t, "정의된 에러 4종이 정확한 조건에서 반환되는지", "Capacity 2, 수신자 없음")
	defer r.Done()

	// New의 용량 검증은 T1에서 확인하므로 여기서는 운영 중 에러만 본다.
	q, _ := New[int](2)

	// 수신자가 없으면 pipe가 1건을 점유하고 나머지는 버퍼에 쌓인다.
	entered := enterPipe(q)
	r.CheckErr(q.Put(1), nil, "여유 있을 때 Put", "성공")
	if !waitSignal(r, entered, "pipe 단계 진입") {
		return
	}
	r.CheckErr(q.Put(2), nil, "정원까지 Put", "성공")
	r.CheckErr(q.Put(3), ErrFull, "정원 초과 Put", "ErrFull")

	q.Close()
	r.CheckErr(q.Put(4), ErrClosed, "Close 후 Put", "ErrClosed")

	var nilQ *Queue[int]
	r.CheckErr(nilQ.Put(1), ErrNil, "nil 큐 Put", "ErrNil")
}

// ---------------------------------------------------------------------------
// T4. nil 리시버 안전성
// ---------------------------------------------------------------------------

func TestQueue_NilReceiver(t *testing.T) {
	r := testreport.New(t, "nil 큐에 대한 모든 공개 메서드가 패닉 없이 방어되는지", "초기화하지 않은 *Queue 포인터")
	defer r.Done()

	var q *Queue[int]

	r.CheckErr(q.Put(1), ErrNil, "Put", "ErrNil")
	r.Check(q.C() == nil, "C", "nil 채널 반환", "nil이 아닌 채널 반환")
	r.Check(q.Len() == 0 && q.Cap() == 0, "Len/Cap", "둘 다 0",
		fmt.Sprintf("Len=%d Cap=%d", q.Len(), q.Cap()))
	r.Check(!q.IsFull(), "IsFull", "false", "true")
	r.Check(q.IsClosed(), "IsClosed", "true", "false")

	safe := func() (ok bool) {
		defer func() { ok = recover() == nil }()
		q.Use(func(c *Context[int]) {})
		q.Close()
		return
	}()
	r.Check(safe, "Use/Close", "패닉 없음", "패닉 발생")
}

// ---------------------------------------------------------------------------
// T5. 적재량과 용량
//
// pipe 고루틴이 꺼내 아직 C()로 전달하지 못한 항목(in-flight)은
// inCh에도 outCh에도 없으므로, 집계에서 빠지면 Len이 실제보다 적게 보고된다.
// ---------------------------------------------------------------------------

func TestQueue_LenCap(t *testing.T) {
	r := testreport.New(t, "in-flight 항목 집계와 Close 이후 카운터 정리 확인", "Capacity 4, 수신자 없음")
	defer r.Done()

	q, _ := New[int](4)
	entered := enterPipe(q)

	r.Check(q.Len() == 0 && q.Cap() == 4, "초기 상태", "Len=0 Cap=4",
		fmt.Sprintf("Len=%d Cap=%d", q.Len(), q.Cap()))

	if q.Put(1) != nil {
		t.Fatalf("Put 실패")
	}
	if !waitSignal(r, entered, "pipe 단계 진입") {
		return
	}

	// 이 시점에 항목은 inCh에서 빠졌지만 아직 전달되지 않았다.
	inFlight := q.Len()
	r.Check(inFlight == 1, "in-flight 집계", "Len=1 (버퍼에 없어도 집계됨)",
		fmt.Sprintf("Len=%d", inFlight))

	for i := 2; i <= 4; i++ {
		if err := q.Put(i); err != nil {
			t.Fatalf("Put(%d) 실패: %v", i, err)
		}
	}
	full := q.Len()
	r.Check(full == 4 && full <= q.Cap(), "정원까지 적재", "Len=4, Cap 초과 없음",
		fmt.Sprintf("Len=%d Cap=%d", full, q.Cap()))
	r.Check(q.IsFull(), "정원 도달 시 IsFull", "true", "false")

	q.Close()
	// outCh가 닫히면 pipe 고루틴이 종료된 것이므로 카운터 정리도 끝나 있다.
	for range q.C() {
	}
	closed := q.Len()
	r.Check(closed == 0, "Close 후 카운터", "0으로 정리됨 (음수 아님)",
		fmt.Sprintf("Len=%d", closed))
}

// ---------------------------------------------------------------------------
// T6. IsFull과 Put의 판정 일치
// ---------------------------------------------------------------------------

func TestQueue_IsFullMatchesPut(t *testing.T) {
	r := testreport.New(t, "IsFull과 Put이 같은 기준으로 수용 여부를 판정하는지", "Capacity 2, 수신자 없음")
	defer r.Done()

	q, _ := New[int](2)
	defer q.Close()

	// 첫 항목이 in-flight가 되어 버퍼와 총 적재량이 어긋난 상태를 만든다.
	entered := enterPipe(q)
	if q.Put(0) != nil {
		t.Fatalf("Put 실패")
	}
	if !waitSignal(r, entered, "pipe 단계 진입") {
		return
	}

	mismatch := 0
	for i := 1; i <= 3; i++ {
		full := q.IsFull()
		err := q.Put(i)
		switch {
		case full && err != ErrFull:
			mismatch++
		case !full && err != nil:
			mismatch++
		}
	}
	r.Check(mismatch == 0, "판정 일치", "IsFull과 Put이 3개 상태에서 모두 같은 결론",
		fmt.Sprintf("%d개 상태에서 불일치", mismatch))

	r.Check(q.IsFull() && q.Put(99) == ErrFull, "정원 도달 상태",
		"IsFull=true이고 Put이 ErrFull",
		fmt.Sprintf("IsFull=%v Put=%v", q.IsFull(), q.Put(99)))
}

// ---------------------------------------------------------------------------
// T7. Use 미들웨어 체인
// ---------------------------------------------------------------------------

func TestQueue_Middleware(t *testing.T) {
	r := testreport.New(t, "Use 체인의 단계별 실행·접근자·중단 불가 설계 확인", "Capacity 4, 소비자 1개")
	defer r.Done()

	q, _ := New[int](4)
	defer q.Close()

	var putCount, pipeCount atomic.Int64
	var order []string
	var orderMu sync.Mutex

	q.Use(func(c *Context[int]) {
		if c.Action() == ActionPut {
			orderMu.Lock()
			order = append(order, "mw1-before")
			orderMu.Unlock()
		}
		c.Next()
		if c.Action() == ActionPut {
			orderMu.Lock()
			order = append(order, "mw1-after")
			orderMu.Unlock()
		}
	})
	q.Use(nil)
	// Next를 호출하지 않는 미들웨어. 체인은 중단되지 않아야 한다.
	q.Use(func(c *Context[int]) {
		if c.Action() == ActionPut {
			orderMu.Lock()
			order = append(order, "mw2-noNext")
			orderMu.Unlock()
		}
	})

	// Put 단계 미들웨어는 Put을 호출한 고루틴에서 동기 실행되므로
	// 아래 두 변수는 테스트 고루틴에서만 접근된다.
	var putData int
	var putErrSeen error
	var putErrObserved bool
	q.Use(func(c *Context[int]) {
		switch c.Action() {
		case ActionPut:
			putCount.Add(1)
			putData = c.Data()
			c.Next()
			putErrSeen, putErrObserved = c.Err(), true
		case ActionPipe:
			pipeCount.Add(1)
			c.Next()
		}
	})

	received := make(chan int, 1)
	go func() { received <- <-q.C() }()

	err := q.Put(42)

	orderMu.Lock()
	got := append([]string(nil), order...)
	orderMu.Unlock()
	want := []string{"mw1-before", "mw2-noNext", "mw1-after"}
	r.Check(err == nil && equalStrings(got, want), "Put 단계 실행 순서",
		fmt.Sprintf("%v", want), fmt.Sprintf("%v (err=%v)", got, err))

	r.Check(err == nil, "nil 핸들러", "건너뛰고 정상 진행", fmt.Sprintf("err=%v", err))
	r.Check(putData == 42, "Put 단계 Data 접근자", "Data=42",
		fmt.Sprintf("Data=%d", putData))

	select {
	case v := <-received:
		r.Check(v == 42, "Next 미호출 시 종단 실행",
			"중단되지 않고 실제 전달됨 (설계 확인)", fmt.Sprintf("수신값 %d", v))
	case <-time.After(3 * time.Second):
		r.Check(false, "Next 미호출 시 종단 실행", "", "3초 내 전달되지 않음")
	}

	r.Check(putCount.Load() == 1 && pipeCount.Load() == 1, "Put/Pipe 양쪽 실행",
		"등록한 미들웨어가 두 단계에서 각각 1회 실행됨",
		fmt.Sprintf("Put %d회 Pipe %d회", putCount.Load(), pipeCount.Load()))

	r.Check(putErrObserved && putErrSeen == nil, "Next 이후 결과 관찰",
		"성공한 Put의 Err이 nil로 관찰됨",
		fmt.Sprintf("관찰 여부=%v Err=%v", putErrObserved, putErrSeen))
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
// T8. Close 의미론과 자원 정리
//
// Close 시 잔여 데이터를 버리는 것은 의도된 동작이다. 끝까지 전달하려면
// 출력 채널을 비워 줄 수신자가 필요한데, 종료 시점에 수신자가 없으면
// pipe 고루틴이 송신 지점에서 영원히 대기하며 누수되기 때문이다.
// ---------------------------------------------------------------------------

func TestQueue_CloseSemantics(t *testing.T) {
	r := testreport.New(t, "Close의 잔여 데이터 폐기와 고루틴 정리 확인", "Capacity 4, 수신자 없음")
	defer r.Done()

	runtime.GC()
	base := runtime.NumGoroutine()

	q, _ := New[int](4)
	afterNew := runtime.NumGoroutine()
	r.Check(afterNew > base, "New의 pipe 고루틴", "생성 시 내부 고루틴 1개 기동",
		fmt.Sprintf("고루틴 수 변화 없음 (%d)", base))

	accepted := 0
	for i := 0; i < 4; i++ {
		if q.Put(i) == nil {
			accepted++
		}
	}
	r.Check(accepted == 4, "Close 이전 적재", "4건 모두 Put 성공",
		fmt.Sprintf("%d건만 성공", accepted))

	q.Close()

	// outCh가 닫힐 때까지 수신하면 pipe 고루틴 종료를 확정할 수 있다.
	drained := 0
	for range q.C() {
		drained++
	}
	r.Check(drained < accepted, "잔여 데이터 폐기",
		fmt.Sprintf("Put 4건 중 %d건만 전달되고 나머지는 폐기됨 (의도된 동작)", drained),
		fmt.Sprintf("%d건이 모두 전달됨", drained))

	r.Check(q.IsClosed() && q.Put(9) == ErrClosed, "Close 후 Put 차단",
		"IsClosed=true, Put은 ErrClosed", "차단되지 않음")

	// 고루틴이 실제로 회수되었는지 확인한다.
	settled := false
	for i := 0; i < 100; i++ {
		if runtime.NumGoroutine() <= base {
			settled = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	r.Check(settled, "Close 후 고루틴 회수",
		fmt.Sprintf("기준선 %d개로 복귀", base),
		fmt.Sprintf("기준선 %d개, 현재 %d개", base, runtime.NumGoroutine()))
}

// ---------------------------------------------------------------------------
// T9. 동시성 안전성 및 처리 성능
//
// 1단계는 Capacity 1로 정원 경계를 최대한 압박하며 안전성만 본다.
// 2단계는 소비자를 두어 연산이 실제로 성공하는 조건에서 처리량을 측정한다.
// 성능 수치는 머신 사양에 좌우되므로 단언하지 않고 참고로만 출력한다.
// ---------------------------------------------------------------------------

func TestQueue_Concurrency(t *testing.T) {
	r := testreport.New(t,
		"멀티 고루틴 경합 안전성(1단계)과 처리 성능(2단계) 확인",
		"1단계 Capacity 1 + 중간 Close / 2단계 Capacity 충분")
	defer r.Done()

	concurrencyStress(t, r)
	concurrencyPerf(t, r)
}

// 1단계: Capacity 1, 극한 경합 + 경합 중 Close. 안전성만 검증한다.
func concurrencyStress(t *testing.T, r *testreport.Report) {
	const producers = 100
	const loops = 300

	q, _ := New[int](1)

	var putOK, putFull, putClosed atomic.Uint64
	var unexpected atomic.Uint64
	var receivedCnt atomic.Uint64

	// 소비자는 채널이 닫힐 때까지 계속 받는다.
	consumerDone := make(chan struct{})
	go func() {
		defer close(consumerDone)
		for range q.C() {
			receivedCnt.Add(1)
		}
	}()

	var wg sync.WaitGroup
	start := make(chan struct{})
	for p := 0; p < producers; p++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			<-start
			for j := 0; j < loops; j++ {
				switch err := q.Put(p*loops + j); err {
				case nil:
					putOK.Add(1)
				case ErrFull:
					putFull.Add(1)
				case ErrClosed:
					putClosed.Add(1)
				default:
					unexpected.Add(1)
				}
			}
		}(p)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		time.Sleep(2 * time.Millisecond)
		q.Close()
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

	<-consumerDone

	expected := uint64(producers * loops)
	total := putOK.Load() + putFull.Load() + putClosed.Load()
	r.Check(total == expected, "1단계 시도 횟수 정합성",
		fmt.Sprintf("Put %d건이 누락 없이 분류됨", expected),
		fmt.Sprintf("분류 합계 %d (기대 %d)", total, expected))

	r.Check(unexpected.Load() == 0, "1단계 예상 외 에러", "없음 (Full/Closed만 발생)",
		fmt.Sprintf("%d건 발생", unexpected.Load()))

	r.Check(q.IsClosed() && putClosed.Load() > 0, "1단계 Close 반영",
		fmt.Sprintf("경합 중 Close 이후 %d건이 ErrClosed로 거부됨", putClosed.Load()),
		fmt.Sprintf("IsClosed=%v, ErrClosed 관측 %d건", q.IsClosed(), putClosed.Load()))

	// Close로 폐기되는 항목이 있으므로 수신 수는 성공 수 이하다.
	r.Check(receivedCnt.Load() <= putOK.Load(), "1단계 수신/성공 관계",
		fmt.Sprintf("성공 %d건 중 %d건 수신 (나머지는 Close로 폐기)", putOK.Load(), receivedCnt.Load()),
		fmt.Sprintf("수신 %d건이 성공 %d건을 초과", receivedCnt.Load(), putOK.Load()))

	r.Check(q.Len() == 0, "1단계 종료 후 카운터", "0으로 정리됨", fmt.Sprintf("Len=%d", q.Len()))

	r.Note("1단계  Capacity 1, 생산자 %d개, 총 %d회 Put, 소요 %v",
		producers, expected, time.Since(begin).Round(time.Millisecond))
}

// 2단계: 소비자를 두어 실제로 전달이 성사되는 조건에서 처리량을 측정한다.
func concurrencyPerf(t *testing.T, r *testreport.Report) {
	const producers = 8
	const loops = 5000
	const total = producers * loops

	measure := func(q *Queue[int]) (time.Duration, uint64) {
		var received atomic.Uint64
		consumerDone := make(chan struct{})
		go func() {
			defer close(consumerDone)
			for range q.C() {
				received.Add(1)
			}
		}()

		var wg sync.WaitGroup
		start := make(chan struct{})
		for p := 0; p < producers; p++ {
			wg.Add(1)
			go func(p int) {
				defer wg.Done()
				<-start
				for j := 0; j < loops; j++ {
					// 정원이 넉넉해도 소비 속도에 따라 일시적으로 찰 수 있어 재시도한다.
					for q.Put(p*loops+j) == ErrFull {
						runtime.Gosched()
					}
				}
			}(p)
		}
		begin := time.Now()
		close(start)
		wg.Wait()
		d := time.Since(begin)
		q.Close()
		<-consumerDone
		return d, received.Load()
	}

	// 워밍업: 첫 측정이 런타임/스케줄러 예열 비용을 떠안지 않도록 한다.
	warm, _ := New[int](1024)
	_, _ = measure(warm)

	plain, _ := New[int](1024)
	plainDur, plainRecv := measure(plain)

	mw, _ := New[int](1024)
	for i := 0; i < 3; i++ {
		mw.Use(func(c *Context[int]) { c.Next() })
	}
	mwDur, _ := measure(mw)

	r.Check(plainRecv > 0 && plainRecv <= total, "2단계 전달 성사",
		fmt.Sprintf("%d건 Put 중 %d건 전달 확인", total, plainRecv),
		fmt.Sprintf("전달 %d건 (기대 1..%d)", plainRecv, total))

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

	r.Note("2단계  Capacity 1024, 생산자 %d개, 총 %d회 Put", producers, total)
	r.Note("Put→C() %-16s %s", perOp(plainDur), throughput(plainDur))
	r.Note("미들웨어 0개 → 3개  %v → %v%s",
		(plainDur / total).Round(time.Nanosecond), (mwDur / total).Round(time.Nanosecond), overhead)
	r.Note("(-race 실행 시 위 수치는 수 배 부풀려짐)")
}
