// Package testreport은 테스트 결과를 항목 단위로 모아 일정한 형식으로 출력한다.
//
// 각 테스트는 Report에 검증 항목을 누적하고, 종료 시 통과 항목을 먼저,
// 실패 항목을 나중에 출력한다. TestMain에서 Main을 호출하면 전체 요약을 같은 순서로 낸다.
//
// 이 패키지는 테스트에서만 사용한다. internal 아래에 두어 외부에 노출되지 않는다.
// 요약 상태는 패키지 전역이지만, Go는 테스트 패키지마다 별도 바이너리를 만들므로
// 패키지 간에 섞이지 않는다.
//
// 사용 예:
//
//	func TestMain(m *testing.M) { testreport.Main(m, "hashmap") }
//
//	func TestSomething(t *testing.T) {
//		r := testreport.New(t, "무엇을 검증하는지", "어떤 조건에서")
//		defer r.Done()
//
//		r.CheckErr(err, ErrInvalidCap, "New(0)", "ErrInvalidCap")
//		r.Check(len(v) == 3, "항목 수", "3건", fmt.Sprintf("%d건", len(v)))
//		r.Note("처리량 %v", d)
//	}
package testreport

import (
	"fmt"
	"os"
	"sync"
	"testing"
)

// nameWidth는 항목 이름 열의 폭이다. 가장 긴 테스트 이름이 들어갈 만큼 잡는다.
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
	if n <= 0 {
		return ""
	}
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

// Report는 한 테스트 함수의 검증 항목을 모은다.
type Report struct {
	t       *testing.T
	purpose string
	cond    string
	passed  []checkItem
	failed  []checkItem
	notes   []string
}

// New는 목적과 조건을 명시한 Report를 만든다. 반환된 Report는 Done으로 마감한다.
func New(t *testing.T, purpose, cond string) *Report {
	return &Report{t: t, purpose: purpose, cond: cond}
}

// Check는 ok가 참이면 통과, 거짓이면 실패로 기록하고 테스트를 실패시킨다.
// passDetail은 통과 시, failDetail은 실패 시 이름 옆에 출력된다.
func (r *Report) Check(ok bool, name, passDetail, failDetail string) {
	if ok {
		r.passed = append(r.passed, checkItem{name, passDetail})
		return
	}
	r.failed = append(r.failed, checkItem{name, failDetail})
	r.t.Errorf("%s: %s", name, failDetail)
}

// CheckErr는 에러 값이 기대와 일치하는지 확인한다.
func (r *Report) CheckErr(got, want error, name, passDetail string) {
	r.Check(got == want, name, passDetail, fmt.Sprintf("%v 기대, 실제 %v", want, got))
}

// Note는 단언하지 않는 참고 수치를 기록한다.
// 성능처럼 머신 사양에 좌우되는 값은 단언 대신 여기에 남긴다.
func (r *Report) Note(format string, args ...any) {
	r.notes = append(r.notes, fmt.Sprintf(format, args...))
}

// Done은 누적된 항목을 출력하고 요약에 등록한다. defer로 호출한다.
func (r *Report) Done() {
	t := r.t
	t.Logf("── %s %s", t.Name(), spaces(54-len(t.Name())))
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

// Main은 테스트를 실행하고 요약을 출력한 뒤 프로세스를 종료한다.
// TestMain에서 이 함수만 호출하면 된다.
func Main(m *testing.M, pkg string) {
	code := m.Run()
	printSummary(pkg)
	os.Exit(code)
}

func printSummary(pkg string) {
	summaryMu.Lock()
	defer summaryMu.Unlock()

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
	fmt.Printf(" %s 테스트 요약\n", pkg)
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
}
