package hashmap

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// 결과 출력 도우미
//
// 각 테스트는 검증 항목을 report에 누적하고, 종료 시 통과 항목을 먼저,
// 실패 항목을 나중에 출력한다. TestMain은 전체 요약을 같은 순서로 낸다.
// ---------------------------------------------------------------------------

const nameWidth = 28

// runeWidth는 한글/한자 등 2칸을 차지하는 문자를 구분한다.
// 이름 열 정렬이 어긋나지 않도록 하기 위함이다.
func runeWidth(r rune) int {
	switch {
	case r >= 0x1100 && r <= 0x115F,
		r >= 0x2E80 && r <= 0xA4CF,
		r >= 0xAC00 && r <= 0xD7A3,
		r >= 0xF900 && r <= 0xFAFF,
		r >= 0xFF00 && r <= 0xFF60,
		r >= 0xFFE0 && r <= 0xFFE6:
		return 2
	}
	return 1
}

func padName(s string) string {
	w := 0
	for _, r := range s {
		w += runeWidth(r)
	}
	if w >= nameWidth {
		return s + " "
	}
	return s + spaces(nameWidth-w)
}

func spaces(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = ' '
	}
	return string(b)
}

type checkItem struct {
	name   string
	detail string
}

type report struct {
	t       *testing.T
	purpose string
	cond    string
	passed  []checkItem
	failed  []checkItem
	notes   []string
}

type summaryEntry struct {
	name      string
	pass      int
	fail      int
	firstFail string
}

var (
	summaryMu sync.Mutex
	summaries []summaryEntry
)

func newReport(t *testing.T, purpose, cond string) *report {
	return &report{t: t, purpose: purpose, cond: cond}
}

// check는 ok가 참이면 통과, 거짓이면 실패로 기록하고 테스트를 실패시킨다.
func (r *report) check(ok bool, name, passDetail, failDetail string) {
	if ok {
		r.passed = append(r.passed, checkItem{name, passDetail})
		return
	}
	r.failed = append(r.failed, checkItem{name, failDetail})
	r.t.Errorf("%s: %s", name, failDetail)
}

// checkErr는 에러 값이 기대와 일치하는지 확인한다.
func (r *report) checkErr(got, want error, name, passDetail string) {
	r.check(got == want, name, passDetail, fmt.Sprintf("%v 기대, 실제 %v", want, got))
}

// note는 단언하지 않는 참고 수치를 기록한다.
func (r *report) note(format string, args ...any) {
	r.notes = append(r.notes, fmt.Sprintf(format, args...))
}

func (r *report) done() {
	t := r.t
	t.Logf("── %s %s", t.Name(), spaces(max(0, 54-len(t.Name()))))
	t.Logf("   목적 : %s", r.purpose)
	t.Logf("   조건 : %s", r.cond)

	if len(r.passed) > 0 {
		t.Logf("")
		t.Logf("   [PASS] %d건", len(r.passed))
		for _, c := range r.passed {
			t.Logf("     · %s%s", padName(c.name), c.detail)
		}
	}
	if len(r.failed) > 0 {
		t.Logf("")
		t.Logf("   [FAIL] %d건", len(r.failed))
		for _, c := range r.failed {
			t.Logf("     · %s%s", padName(c.name), c.detail)
		}
	}
	if len(r.notes) > 0 {
		t.Logf("")
		t.Logf("   [측정] 참고용, 단언 아님")
		for _, n := range r.notes {
			t.Logf("     · %s", n)
		}
	}

	total := len(r.passed) + len(r.failed)
	t.Logf("")
	t.Logf("   결과 : %d/%d 통과", len(r.passed), total)

	e := summaryEntry{name: t.Name(), pass: len(r.passed), fail: len(r.failed)}
	if len(r.failed) > 0 {
		e.firstFail = r.failed[0].name
	}
	summaryMu.Lock()
	summaries = append(summaries, e)
	summaryMu.Unlock()
}

func TestMain(m *testing.M) {
	code := m.Run()

	var passList, failList []summaryEntry
	var totalPass, totalFail int
	for _, s := range summaries {
		totalPass += s.pass
		totalFail += s.fail
		if s.fail > 0 {
			failList = append(failList, s)
		} else {
			passList = append(passList, s)
		}
	}

	line := "========================================================="
	fmt.Println()
	fmt.Println(line)
	fmt.Println(" hashmap 테스트 요약")
	fmt.Println(line)

	if len(passList) > 0 {
		fmt.Printf(" [PASS] %d개\n", len(passList))
		for _, s := range passList {
			fmt.Printf("   %s%d/%d\n", padName(s.name), s.pass, s.pass+s.fail)
		}
	}
	if len(failList) > 0 {
		fmt.Printf(" [FAIL] %d개\n", len(failList))
		for _, s := range failList {
			fmt.Printf("   %s%d/%d    %s\n", padName(s.name), s.pass, s.pass+s.fail, s.firstFail)
		}
	}

	fmt.Println("---------------------------------------------------------")
	fmt.Printf(" %d개 함수 / 검증 %d항목 / 통과 %d / 실패 %d\n",
		len(summaries), totalPass+totalFail, totalPass, totalFail)
	fmt.Println(line)

	os.Exit(code)
}

// ---------------------------------------------------------------------------
// T1. 생성과 소멸
// ---------------------------------------------------------------------------

func TestHashMap_NewAndClose(t *testing.T) {
	r := newReport(t, "New의 용량 검증과 Close 이후 상태 전이 확인", "Capacity 10, 단일 고루틴")
	defer r.done()

	_, err := New[int, int](0)
	r.checkErr(err, ErrInvalidCap, "New(0)", "ErrInvalidCap")

	_, err = New[int, int](-1)
	r.checkErr(err, ErrInvalidCap, "New(-1)", "ErrInvalidCap")

	hm, err := New[int, int](10)
	if err != nil {
		t.Fatalf("New(10) 실패: %v", err)
	}
	r.check(hm.Len() == 0 && hm.Cap() == 10 && !hm.IsClosed(),
		"생성 직후 상태", "Len=0 Cap=10 IsClosed=false",
		fmt.Sprintf("Len=%d Cap=%d IsClosed=%v", hm.Len(), hm.Cap(), hm.IsClosed()))

	hm.Close()
	r.check(hm.IsClosed() && hm.Len() == 0 && hm.Cap() == 0,
		"Close 후 상태", "IsClosed=true Len=0 Cap=0",
		fmt.Sprintf("IsClosed=%v Len=%d Cap=%d", hm.IsClosed(), hm.Len(), hm.Cap()))

	// 중복 Close와 Close 이후 부가 호출이 패닉을 내지 않아야 한다.
	safe := func() (ok bool) {
		defer func() { ok = recover() == nil }()
		hm.Close()
		hm.Delete(1)
		hm.Use(func(c *Context[int, int]) { c.Next() })
		return
	}()
	r.check(safe, "Close 후 중복 호출", "Close/Delete/Use 모두 패닉 없음", "패닉 발생")
}

// ---------------------------------------------------------------------------
// T2. 기본 연산 정상 경로
// ---------------------------------------------------------------------------

func TestHashMap_BasicOps(t *testing.T) {
	r := newReport(t, "Put/Get/Delete의 정상 경로 동작 확인", "Capacity 10, 단일 고루틴")
	defer r.done()

	hm, _ := New[int, string](10)
	defer hm.Close()

	err := hm.Put(1, "one")
	v, getErr := hm.Get(1)
	r.check(err == nil && getErr == nil && v == "one",
		"Put 후 Get", "저장한 값이 그대로 조회됨",
		fmt.Sprintf("put=%v get=%v value=%q", err, getErr, v))

	hm.Delete(1)
	_, getErr = hm.Get(1)
	r.checkErr(getErr, ErrKeyNotFound, "Delete 후 Get", "ErrKeyNotFound")

	before := hm.Len()
	hm.Delete(999)
	r.check(hm.Len() == before, "미존재 키 Delete",
		fmt.Sprintf("Len 불변 (%d)", before),
		fmt.Sprintf("Len이 %d에서 %d로 변함", before, hm.Len()))

	err = hm.Put(1, "again")
	v, _ = hm.Get(1)
	r.check(err == nil && v == "again", "삭제한 키 재삽입", "정상 저장됨",
		fmt.Sprintf("err=%v value=%q", err, v))
}

// ---------------------------------------------------------------------------
// T3. 에러 경로 전수
// ---------------------------------------------------------------------------

func TestHashMap_Errors(t *testing.T) {
	r := newReport(t, "정의된 에러 7종이 정확한 조건에서 반환되는지", "Capacity 3, 단일 고루틴")
	defer r.done()

	hm, _ := New[int, int](3)

	_ = hm.Put(1, 100)
	err := hm.Put(1, 200)
	kept, _ := hm.Get(1)
	r.check(err == ErrDupKey && kept == 100, "중복 키 Put",
		"ErrDupKey (기존 값 보존)",
		fmt.Sprintf("err=%v, 기존 값=%d", err, kept))

	_ = hm.Put(2, 200)
	_ = hm.Put(3, 300)
	r.checkErr(hm.Put(4, 400), ErrFull, "용량 초과 Put", "ErrFull")

	// 용량 검사가 중복 키 검사보다 먼저 수행된다.
	r.checkErr(hm.Put(1, 999), ErrFull, "가득 찬 맵 + 중복 키", "ErrFull (용량 검사 우선)")

	_, err = hm.Get(999)
	_, doErr := hm.Do(999, func(k, v int) (int, error) { return v, nil })
	r.check(err == ErrKeyNotFound && doErr == ErrKeyNotFound,
		"미존재 키 Get/Do", "ErrKeyNotFound",
		fmt.Sprintf("get=%v do=%v", err, doErr))

	_, err = hm.Do(1, nil)
	_, doErr = hm.DoAll(nil)
	r.check(err == ErrNilCallback && doErr == ErrNilCallback,
		"nil 콜백 Do/DoAll", "ErrNilCallback",
		fmt.Sprintf("do=%v doAll=%v", err, doErr))

	hm.Close()
	e1 := hm.Put(10, 10)
	_, e2 := hm.Get(1)
	_, e3 := hm.Do(1, func(k, v int) (int, error) { return v, nil })
	_, e4 := hm.DoAll(func(k, v int) (int, error) { return v, nil })
	r.check(e1 == ErrClosed && e2 == ErrClosed && e3 == ErrClosed && e4 == ErrClosed,
		"Close 후 4개 메서드", "ErrClosed",
		fmt.Sprintf("put=%v get=%v do=%v doAll=%v", e1, e2, e3, e4))

	// 닫힘 검사가 nil 콜백 검사보다 먼저 수행된다.
	_, err = hm.Do(1, nil)
	r.checkErr(err, ErrClosed, "닫힌 맵 + nil 콜백", "ErrClosed (닫힘 검사 우선)")
}

// ---------------------------------------------------------------------------
// T4. nil 리시버 안전성
// ---------------------------------------------------------------------------

func TestHashMap_NilReceiver(t *testing.T) {
	r := newReport(t, "nil 맵에 대한 모든 공개 메서드가 패닉 없이 방어되는지", "초기화하지 않은 *Map 포인터")
	defer r.done()

	var hm *Map[int, string]

	r.checkErr(hm.Put(1, "a"), ErrNil, "Put", "ErrNil")

	v, err := hm.Get(1)
	r.check(err == ErrNil && v == "", "Get", "ErrNil, 제로값 반환",
		fmt.Sprintf("err=%v value=%q", err, v))

	_, err = hm.Do(1, func(k int, v string) (int, error) { return 0, nil })
	r.checkErr(err, ErrNil, "Do", "ErrNil")

	_, err = hm.DoAll(func(k int, v string) (int, error) { return 0, nil })
	r.checkErr(err, ErrNil, "DoAll", "ErrNil")

	r.check(hm.Len() == 0 && hm.Cap() == 0, "Len/Cap", "둘 다 0",
		fmt.Sprintf("Len=%d Cap=%d", hm.Len(), hm.Cap()))

	r.check(hm.IsClosed(), "IsClosed", "true", "false")

	count := 0
	for range hm.All() {
		count++
	}
	r.check(count == 0, "All", "0회 순회", fmt.Sprintf("%d회 순회", count))

	safe := func() (ok bool) {
		defer func() { ok = recover() == nil }()
		hm.Delete(1)
		hm.Use(func(c *Context[int, string]) {})
		hm.Close()
		return
	}()
	r.check(safe, "Delete/Use/Close", "패닉 없음", "패닉 발생")
}

// ---------------------------------------------------------------------------
// T5. 개수와 용량
// ---------------------------------------------------------------------------

func TestHashMap_LenCap(t *testing.T) {
	r := newReport(t, "삽입·삭제·Close에 따른 Len/Cap 변화 확인", "Capacity 5, 단일 고루틴")
	defer r.done()

	hm, _ := New[int, int](5)

	r.check(hm.Len() == 0 && hm.Cap() == 5, "초기 상태", "Len=0 Cap=5",
		fmt.Sprintf("Len=%d Cap=%d", hm.Len(), hm.Cap()))

	for i := 1; i <= 3; i++ {
		_ = hm.Put(i, i)
	}
	r.check(hm.Len() == 3, "3건 삽입", "Len=3", fmt.Sprintf("Len=%d", hm.Len()))

	hm.Delete(1)
	r.check(hm.Len() == 2, "1건 삭제", "Len=2", fmt.Sprintf("Len=%d", hm.Len()))

	hm.Delete(999)
	r.check(hm.Len() == 2, "미존재 키 삭제", "Len 불변 (2)", fmt.Sprintf("Len=%d", hm.Len()))

	r.check(hm.Cap() == 5, "삽입·삭제 중 Cap", "5로 불변", fmt.Sprintf("Cap=%d", hm.Cap()))

	hm.Close()
	r.check(hm.Len() == 0 && hm.Cap() == 0, "Close 후", "Len=0 Cap=0",
		fmt.Sprintf("Len=%d Cap=%d", hm.Len(), hm.Cap()))
}

// ---------------------------------------------------------------------------
// T6. All 순회
// ---------------------------------------------------------------------------

func TestHashMap_Iteration(t *testing.T) {
	r := newReport(t, "All 반복자의 순회·조기 종료·락 해제 확인", "Capacity 10, 5건 저장")
	defer r.done()

	hm, _ := New[int, int](10)
	defer hm.Close()
	for i := 1; i <= 5; i++ {
		_ = hm.Put(i, i*10)
	}

	sum, count := 0, 0
	for _, v := range hm.All() {
		sum += v
		count++
	}
	r.check(count == 5 && sum == 150, "전체 순회", "5건 / 합계 150",
		fmt.Sprintf("%d건 / 합계 %d", count, sum))

	visited := 0
	for range hm.All() {
		visited++
		break
	}
	// break로 중단해도 내부 RLock이 해제되어야 이후 쓰기가 가능하다.
	done := make(chan error, 1)
	go func() { done <- hm.Put(100, 100) }()
	select {
	case err := <-done:
		r.check(visited == 1 && err == nil, "조기 break 후 쓰기",
			"1건만 순회, 이후 Put 정상 (락 해제 확인)",
			fmt.Sprintf("순회 %d건, Put=%v", visited, err))
	case <-time.After(3 * time.Second):
		r.check(false, "조기 break 후 쓰기", "", "데드락: RLock이 해제되지 않음")
	}

	var toDelete []int
	for k, v := range hm.All() {
		if v >= 40 {
			toDelete = append(toDelete, k)
		}
	}
	for _, k := range toDelete {
		hm.Delete(k)
	}
	_, err := hm.Get(5)
	r.check(err == ErrKeyNotFound, "순회 후 외부 삭제", "수집한 키가 정상 삭제됨",
		fmt.Sprintf("삭제 대상 %d건, Get=%v", len(toDelete), err))

	for _, v := range hm.All() {
		v = 0
		_ = v
	}
	kept, _ := hm.Get(1)
	r.check(kept == 10, "순회 값 수정", "복사본이라 맵에 반영되지 않음",
		fmt.Sprintf("원본이 %d로 변경됨", kept))
}

// ---------------------------------------------------------------------------
// T7. Do / DoAll 콜백
// ---------------------------------------------------------------------------

func TestHashMap_Callback(t *testing.T) {
	r := newReport(t, "Do/DoAll의 반환값 전달과 에러 단축 평가 확인", "Capacity 10, 5건 저장")
	defer r.done()

	hm, _ := New[int, int](10)
	defer hm.Close()
	for i := 1; i <= 5; i++ {
		_ = hm.Put(i, i)
	}

	res, err := hm.Do(3, func(k, v int) (int, error) { return v * 2, nil })
	r.check(err == nil && res == 6, "Do 반환값 전달", "콜백 결과 6이 그대로 반환됨",
		fmt.Sprintf("res=%d err=%v", res, err))

	sum, err := hm.DoAll(func(k, v int) (int, error) { return v, nil })
	r.check(err == nil && sum == 15, "DoAll 합계", "1..5 합계 15",
		fmt.Sprintf("sum=%d err=%v", sum, err))

	errStop := errors.New("stop")
	calls := 0
	partial, err := hm.DoAll(func(k, v int) (int, error) {
		calls++
		if calls == 3 {
			return 0, errStop
		}
		return 1, nil
	})
	r.check(errors.Is(err, errStop) && calls == 3 && partial == 2,
		"DoAll 에러 단축 평가", "3번째 호출에서 중단, 그때까지 합계 2",
		fmt.Sprintf("calls=%d partial=%d err=%v", calls, partial, err))

	_, _ = hm.Do(1, func(k, v int) (int, error) {
		v = 999
		return v, nil
	})
	kept, _ := hm.Get(1)
	r.check(kept == 1, "콜백 내 값 수정", "복사본이라 맵에 반영되지 않음",
		fmt.Sprintf("원본이 %d로 변경됨", kept))
}

// ---------------------------------------------------------------------------
// T8. Use 미들웨어 체인
// ---------------------------------------------------------------------------

func TestHashMap_Middleware(t *testing.T) {
	r := newReport(t, "Use 체인의 실행 순서·Context 접근자·중단 불가 설계 확인", "미들웨어 3개(nil 포함) 등록")
	defer r.done()

	hm, _ := New[int, string](10)
	defer hm.Close()

	var order []string
	hm.Use(func(c *Context[int, string]) {
		order = append(order, "mw1-before")
		c.Next()
		order = append(order, "mw1-after")
	})
	hm.Use(nil)
	// Next를 호출하지 않는 미들웨어. 체인은 중단되지 않아야 한다.
	hm.Use(func(c *Context[int, string]) {
		order = append(order, "mw2-noNext")
	})

	var act ActionType
	var gotKey int
	var gotVal string
	hm.Use(func(c *Context[int, string]) {
		act, gotKey, gotVal = c.Action(), c.Key(), c.Value()
	})

	err := hm.Put(7, "seven")

	// 접근자 값은 이후 다른 연산에서 덮이므로 Put 직후에 확인한다.
	r.check(act == ActionPut && gotKey == 7 && gotVal == "seven", "Put 단계 접근자",
		"Action=Put Key=7 Value=seven",
		fmt.Sprintf("Action=%v Key=%d Value=%q", act, gotKey, gotVal))

	want := []string{"mw1-before", "mw2-noNext", "mw1-after"}
	r.check(err == nil && equalStrings(order, want), "실행 순서",
		fmt.Sprintf("%v", want), fmt.Sprintf("%v (err=%v)", order, err))

	r.check(err == nil, "nil 핸들러", "건너뛰고 정상 진행", fmt.Sprintf("err=%v", err))

	v, getErr := hm.Get(7)
	r.check(getErr == nil && v == "seven", "Next 미호출 시 종단 실행",
		"중단되지 않고 실제 저장됨 (설계 확인)",
		fmt.Sprintf("get=%v value=%q", getErr, v))

	var obsAct ActionType
	var obsVal string
	var obsErr error
	hm.Use(func(c *Context[int, string]) {
		c.Next()
		obsAct, obsVal, obsErr = c.Action(), c.Value(), c.Err()
	})

	_, _ = hm.Get(7)
	okGet := obsAct == ActionGet && obsVal == "seven" && obsErr == nil
	_, _ = hm.Get(9999)
	okMiss := obsErr == ErrKeyNotFound
	r.check(okGet && okMiss, "Next 이후 결과 관찰",
		"Get 성공 시 값, 실패 시 ErrKeyNotFound 관찰됨",
		fmt.Sprintf("성공관찰=%v 실패관찰=%v(%v)", okGet, okMiss, obsErr))

	var delAct ActionType
	hm.Use(func(c *Context[int, string]) { delAct = c.Action() })
	hm.Delete(7)
	r.check(delAct == ActionDelete, "Delete 단계 접근자", "Action=Delete",
		fmt.Sprintf("Action=%v", delAct))
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
// 1단계는 Capacity 1로 용량 경계를 최대한 압박하며 안전성만 본다.
// 2단계는 연산이 실제로 성공하는 용량에서 처리 성능을 측정한다.
// 성능 수치는 머신 사양에 좌우되므로 단언하지 않고 참고로만 출력한다.
// ---------------------------------------------------------------------------

func TestHashMap_Concurrency(t *testing.T) {
	r := newReport(t,
		"멀티 고루틴 경합 안전성(1단계)과 성공 경로 처리 성능(2단계) 확인",
		"1단계 Capacity 1 / 2단계 Capacity 충분")
	defer r.done()

	concurrencyStress(t, r)
	concurrencyPerf(t, r)
}

// 1단계: Capacity 1, 극한 경합. 안전성만 검증한다.
func concurrencyStress(t *testing.T, r *report) {
	const groups = 100
	const loops = 300

	hm, _ := New[int, int](1)

	// 연산별로 분리해서 센다. 카운터를 공유하면 합계 검증이 성립하지 않는다.
	var putOK, putRejected, putClosed atomic.Uint64
	var getOK, getMiss, getClosed atomic.Uint64
	var delDone atomic.Uint64
	var unexpected atomic.Uint64

	var wg sync.WaitGroup
	start := make(chan struct{})

	for g := 0; g < groups; g++ {
		wg.Add(3)
		go func(g int) {
			defer wg.Done()
			<-start
			for j := 0; j < loops; j++ {
				switch err := hm.Put(g*loops+j, j); err {
				case nil:
					putOK.Add(1)
				case ErrFull, ErrDupKey:
					putRejected.Add(1)
				case ErrClosed:
					putClosed.Add(1)
				default:
					unexpected.Add(1)
				}
			}
		}(g)
		go func(g int) {
			defer wg.Done()
			<-start
			for j := 0; j < loops; j++ {
				switch _, err := hm.Get(g*loops + j); err {
				case nil:
					getOK.Add(1)
				case ErrKeyNotFound:
					getMiss.Add(1)
				case ErrClosed:
					getClosed.Add(1)
				default:
					unexpected.Add(1)
				}
			}
		}(g)
		go func(g int) {
			defer wg.Done()
			<-start
			for j := 0; j < loops; j++ {
				hm.Delete(g*loops + j)
				delDone.Add(1)
			}
		}(g)
	}

	// 경합이 진행되는 중간에 Close를 끼워 넣는다.
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		time.Sleep(2 * time.Millisecond)
		hm.Close()
	}()

	begin := time.Now()
	close(start)

	finished := make(chan struct{})
	go func() { wg.Wait(); close(finished) }()

	select {
	case <-finished:
		r.check(true, "1단계 완주", fmt.Sprintf("패닉·데드락 없이 %v 내 완료", time.Since(begin).Round(time.Millisecond)), "")
	case <-time.After(60 * time.Second):
		r.check(false, "1단계 완주", "", "60초 내 완료되지 않음 (데드락 의심)")
		return
	}

	// 모든 시도가 정확히 한 분류에 들어가야 한다.
	expected := uint64(groups * loops)
	totalPut := putOK.Load() + putRejected.Load() + putClosed.Load()
	totalGet := getOK.Load() + getMiss.Load() + getClosed.Load()
	r.check(totalPut == expected && totalGet == expected && delDone.Load() == expected,
		"1단계 시도 횟수 정합성",
		fmt.Sprintf("Put/Get/Delete 각 %d건이 누락 없이 분류됨", expected),
		fmt.Sprintf("Put %d, Get %d, Delete %d (기대 각 %d)", totalPut, totalGet, delDone.Load(), expected))

	r.check(unexpected.Load() == 0, "1단계 예상 외 에러", "없음 (Full/DupKey/KeyNotFound/Closed만 발생)",
		fmt.Sprintf("%d건 발생", unexpected.Load()))

	closedTotal := putClosed.Load() + getClosed.Load()
	r.check(hm.IsClosed() && closedTotal > 0, "1단계 Close 반영",
		fmt.Sprintf("경합 중 Close 이후 %d건이 ErrClosed로 거부됨", closedTotal),
		fmt.Sprintf("IsClosed=%v, ErrClosed 관측 %d건", hm.IsClosed(), closedTotal))

	r.note("1단계  Capacity 1, 고루틴 %d개, 총 %d 연산, 소요 %v",
		groups*3+1, uint64(groups*loops*3), time.Since(begin).Round(time.Millisecond))
}

// 2단계: 연산이 성공하는 용량에서 처리 성능을 측정한다.
func concurrencyPerf(t *testing.T, r *report) {
	const workers = 8
	const loops = 5000
	const total = workers * loops

	hm, _ := New[int, int](total + 1)
	defer hm.Close()

	run := func(fn func(w, j int)) time.Duration {
		var wg sync.WaitGroup
		start := make(chan struct{})
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
				<-start
				for j := 0; j < loops; j++ {
					fn(w, j)
				}
			}(w)
		}
		begin := time.Now()
		close(start)
		wg.Wait()
		return time.Since(begin)
	}

	// 워밍업: 첫 측정이 런타임/스케줄러 예열 비용을 떠안지 않도록 한다.
	warm, _ := New[int, int](total + 1)
	_ = run(func(w, j int) { _ = warm.Put(w*loops+j, j) })
	_ = run(func(w, j int) { _, _ = warm.Get(w*loops + j) })
	warm.Close()

	var putFail, getFail atomic.Uint64
	putDur := run(func(w, j int) {
		if hm.Put(w*loops+j, j) != nil {
			putFail.Add(1)
		}
	})
	getDur := run(func(w, j int) {
		if _, err := hm.Get(w*loops + j); err != nil {
			getFail.Add(1)
		}
	})
	delDur := run(func(w, j int) { hm.Delete(w*loops + j) })

	r.check(putFail.Load() == 0 && getFail.Load() == 0 && hm.Len() == 0,
		"2단계 성공 경로", fmt.Sprintf("%d건 Put/Get/Delete 모두 성공", total),
		fmt.Sprintf("put실패=%d get실패=%d 잔여=%d", putFail.Load(), getFail.Load(), hm.Len()))

	// 미들웨어 오버헤드 비교.
	// 위의 getDur는 측정 순서가 달라 예열 상태가 다르므로,
	// 동일 조건에서 나란히 측정한 값끼리 비교한다.
	plainHM, _ := New[int, int](total + 1)
	defer plainHM.Close()
	mwHM, _ := New[int, int](total + 1)
	defer mwHM.Close()
	for i := 0; i < 3; i++ {
		mwHM.Use(func(c *Context[int, int]) { c.Next() })
	}
	for w := 0; w < workers; w++ {
		for j := 0; j < loops; j++ {
			_ = plainHM.Put(w*loops+j, j)
			_ = mwHM.Put(w*loops+j, j)
		}
	}
	plainGetDur := run(func(w, j int) { _, _ = plainHM.Get(w*loops + j) })
	mwGetDur := run(func(w, j int) { _, _ = mwHM.Get(w*loops + j) })

	perOp := func(d time.Duration) string {
		return fmt.Sprintf("평균 %v", (d / total).Round(time.Nanosecond))
	}
	throughput := func(d time.Duration) string {
		if d == 0 {
			return "-"
		}
		return fmt.Sprintf("%.2fM ops/s", float64(total)/d.Seconds()/1e6)
	}

	r.note("2단계  Capacity %d, 고루틴 %d개, 연산별 %d회", total+1, workers, total)
	r.note("Put     %-16s %s", perOp(putDur), throughput(putDur))
	r.note("Get     %-16s %s", perOp(getDur), throughput(getDur))
	r.note("Delete  %-16s %s", perOp(delDur), throughput(delDur))

	overhead := ""
	if plainGetDur > 0 {
		overhead = fmt.Sprintf(" (%+.1f%%)", (float64(mwGetDur)/float64(plainGetDur)-1)*100)
	}
	r.note("미들웨어 0개 → 3개  Get %v → %v%s",
		(plainGetDur / total).Round(time.Nanosecond), (mwGetDur / total).Round(time.Nanosecond), overhead)
	r.note("(-race 실행 시 위 수치는 수 배 부풀려짐)")
}
