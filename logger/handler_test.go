package logger

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/slogtest"

	"github.com/team-core-1/util/internal/testreport"
)

// ---------------------------------------------------------------------------
// 로그 한 줄 파싱
//
// 출력 형식은 "TIME [LEVEL] MSG k=v a.b=v ..." 이다.
// 큰따옴표로 감싼 구간의 공백은 분리하지 않고, 점(.)으로 구분된 키는
// 중첩 map으로 복원한다. 시각은 Record.Time이 0값이면 생략될 수 있다.
// ---------------------------------------------------------------------------

func splitFields(line string) []string {
	var (
		fields  []string
		cur     strings.Builder
		inQuote bool
		escaped bool
	)
	flush := func() {
		if cur.Len() > 0 {
			fields = append(fields, cur.String())
			cur.Reset()
		}
	}
	for _, r := range line {
		switch {
		case escaped:
			cur.WriteRune(r)
			escaped = false
		case inQuote && r == '\\':
			cur.WriteRune(r)
			escaped = true
		case r == '"':
			inQuote = !inQuote
			cur.WriteRune(r)
		case r == ' ' && !inQuote:
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return fields
}

func parseLogLine(line string) map[string]any {
	m := map[string]any{}
	fields := splitFields(line)
	if len(fields) == 0 {
		return m
	}

	i := 0
	if !strings.HasPrefix(fields[i], "[") {
		m[slog.TimeKey] = fields[i]
		i++
	}
	if i < len(fields) && strings.HasPrefix(fields[i], "[") {
		m[slog.LevelKey] = strings.Trim(fields[i], "[]")
		i++
	}

	var msg []string
	for ; i < len(fields); i++ {
		if strings.Contains(fields[i], "=") {
			break
		}
		msg = append(msg, fields[i])
	}
	m[slog.MessageKey] = strings.Join(msg, " ")

	for ; i < len(fields); i++ {
		key, raw, ok := strings.Cut(fields[i], "=")
		if !ok {
			continue
		}
		value := raw
		if unquoted, err := strconv.Unquote(raw); err == nil {
			value = unquoted
		}
		cur := m
		parts := strings.Split(key, ".")
		for _, p := range parts[:len(parts)-1] {
			next, exists := cur[p].(map[string]any)
			if !exists {
				next = map[string]any{}
				cur[p] = next
			}
			cur = next
		}
		cur[parts[len(parts)-1]] = value
	}
	return m
}

// attrsOf는 지정한 메시지가 담긴 줄에서 메시지 이후의 속성 문자열만 뽑는다.
func attrsOf(content, msg string) (string, bool) {
	for _, line := range strings.Split(content, "\n") {
		if idx := strings.Index(line, msg); idx >= 0 {
			return strings.TrimSpace(line[idx+len(msg):]), true
		}
	}
	return "", false
}

// ---------------------------------------------------------------------------
// T7. slog 표준 적합성
// ---------------------------------------------------------------------------

func TestHandler_SlogConformance(t *testing.T) {
	r := testreport.New(t,
		"testing/slogtest 표준 검사로 slog.Handler 규약 준수 확인",
		"Level Debug, 메모리 버퍼 출력")
	defer r.Done()

	var buf bytes.Buffer
	h := newHandler(&buf, &handlerOptions{Level: slog.LevelDebug})

	results := func() []map[string]any {
		var out []map[string]any
		for _, line := range strings.Split(buf.String(), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			out = append(out, parseLogLine(line))
		}
		return out
	}

	err := slogtest.TestHandler(h, results)
	detail := "WithAttrs/WithGroup 한정 범위, 빈 그룹 생략, Resolve 호출, 제로값 Attr·시각 처리 등"
	r.Check(err == nil, "표준 적합성 검사", detail, fmt.Sprintf("%v", err))
}

// ---------------------------------------------------------------------------
// T8. 속성과 그룹
//
// slog 규약상 WithGroup은 그 이후에 추가된 속성만 그룹으로 한정해야 한다.
// 접두사를 핸들러에 하나만 두고 출력 시점에 일괄 적용하면
// WithGroup 이전에 등록된 속성까지 그룹으로 묶이는 오류가 생긴다.
// ---------------------------------------------------------------------------

func TestHandler_AttrsAndGroups(t *testing.T) {
	r := testreport.New(t, "WithAttrs/WithGroup의 한정 범위가 slog 규약과 일치하는지",
		"그룹 전후 속성, 중첩 그룹, 빈 그룹, 그룹 값 속성")
	defer r.Done()

	path := filepath.Join(t.TempDir(), "group.log")
	if err := Init(Config{Path: path, Level: LogLevelInfo}); err != nil {
		t.Fatalf("Init 실패: %v", err)
	}

	slog.Default().With("before", 1).WithGroup("g").With("after", 2).Info("case1", "inline", 3)
	slog.Default().WithGroup("g1").With("x", 1).WithGroup("g2").Info("case2", "y", 2)
	slog.Default().WithGroup("empty").Info("case3")
	slog.Default().Info("case4", slog.Group("gr", "a", 1, "b", 2))
	slog.Default().Info("case5", slog.Group("emptygr"))

	if err := Close(); err != nil {
		t.Fatalf("Close 실패: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("로그 파일 읽기 실패: %v", err)
	}
	content := string(b)

	for _, c := range []struct {
		msg, want, name string
	}{
		{"case1", "before=1 g.after=2 g.inline=3", "그룹 이전 속성 미한정"},
		{"case2", "g1.x=1 g1.g2.y=2", "중첩 그룹 한정"},
		{"case3", "", "속성 없는 그룹 생략"},
		{"case4", "gr.a=1 gr.b=2", "그룹 값 속성 전개"},
		{"case5", "", "빈 그룹 값 속성 생략"},
	} {
		got, ok := attrsOf(content, c.msg)
		if !ok {
			r.Check(false, c.name, "", c.msg+" 로그 줄을 찾지 못함")
			continue
		}
		r.Check(got == c.want, c.name, fmt.Sprintf("%s → %q", c.msg, c.want),
			fmt.Sprintf("%q 기대, 실제 %q", c.want, got))
	}
}

// ---------------------------------------------------------------------------
// T9. 값 이스케이프
//
// 값에 줄바꿈이 그대로 나가면 로그 1건이 여러 줄로 쪼개져,
// 줄 단위 수집기가 가짜 레코드를 정상 레코드로 인식하게 된다(로그 위조).
// ---------------------------------------------------------------------------

func TestHandler_Quoting(t *testing.T) {
	r := testreport.New(t, "구분자·제어 문자 포함 시 인용 처리와 로그 위조 차단 확인",
		"값/키/그룹명/메시지 경로별 검증")
	defer r.Done()

	// 가짜 로그 레코드를 주입하려는 입력
	forged := "normal\n2026-01-01T00:00:00.000 [ERROR] granted user=attacker"

	for _, c := range []struct {
		name string
		log  func(l *slog.Logger)
		want string
	}{
		{"값에 줄바꿈", func(l *slog.Logger) { l.Info("m", "input", forged) },
			`input="normal\n2026-01-01T00:00:00.000 [ERROR] granted user=attacker"`},
		{"값에 공백", func(l *slog.Logger) { l.Info("m", "note", "hello world") }, `note="hello world"`},
		{"값에 등호", func(l *slog.Logger) { l.Info("m", "expr", "a=b") }, `expr="a=b"`},
		{"값에 인용부호", func(l *slog.Logger) { l.Info("m", "q", `he said "hi"`) }, `q="he said \"hi\""`},
		{"빈 값", func(l *slog.Logger) { l.Info("m", "empty", "") }, `empty=""`},
		{"키에 공백", func(l *slog.Logger) { l.Info("m", "my key", "v") }, `"my key"=v`},
		{"그룹명에 공백", func(l *slog.Logger) { l.WithGroup("g p").Info("m", "k", "v") }, `"g p.k"=v`},
		{"인용 불필요한 값", func(l *slog.Logger) { l.Info("m", "user", "alice", "count", 3) }, `user=alice count=3`},
	} {
		var buf bytes.Buffer
		l := slog.New(newHandler(&buf, &handlerOptions{Level: slog.LevelDebug}))
		c.log(l)
		out := strings.TrimSuffix(buf.String(), "\n")

		if strings.Contains(out, "\n") {
			r.Check(false, c.name, "", "로그가 여러 줄로 분리됨 (로그 위조 가능)")
			continue
		}
		idx := strings.Index(out, "] ")
		if idx < 0 {
			r.Check(false, c.name, "", fmt.Sprintf("예상치 못한 출력 형식: %q", out))
			continue
		}
		got := out[idx+2:]
		r.Check(strings.HasSuffix(got, c.want), c.name, got,
			fmt.Sprintf("접미사 %s 기대, 실제 %s", c.want, got))
	}

	// 메시지 경로도 줄 구조를 깨뜨리지 않아야 한다.
	var buf bytes.Buffer
	l := slog.New(newHandler(&buf, &handlerOptions{Level: slog.LevelDebug}))
	l.Info(forged)
	single := !strings.Contains(strings.TrimSuffix(buf.String(), "\n"), "\n")
	r.Check(single, "메시지에 줄바꿈", "인용되어 한 줄 유지", "로그가 여러 줄로 분리됨 (로그 위조 가능)")

	// 메시지는 위치 기반 필드라 공백만으로는 인용하지 않는다(가독성).
	buf.Reset()
	l = slog.New(newHandler(&buf, &handlerOptions{Level: slog.LevelDebug}))
	l.Info("hello world")
	r.Check(strings.Contains(buf.String(), "] hello world"), "메시지의 공백",
		"불필요하게 인용하지 않음", fmt.Sprintf("인용됨: %q", buf.String()))
}

// ---------------------------------------------------------------------------
// T10. 핸들러 경계 조건
//
// 기본값 보정, 빈 인자 단축 경로, 멀티바이트 문자 처리 등
// 일반 사용에서는 드물지만 규약상 지켜져야 하는 동작을 확인한다.
// ---------------------------------------------------------------------------

func TestHandler_EdgeCases(t *testing.T) {
	r := testreport.New(t, "핸들러의 기본값 보정과 경계 입력 처리 확인",
		"nil 옵션 / 빈 인자 / 멀티바이트 문자")
	defer r.Done()

	// nil 옵션이면 기본값이 적용된다.
	var buf bytes.Buffer
	h := newHandler(&buf, nil)
	slog.New(h).Info("기본 옵션")
	out := buf.String()
	r.Check(strings.Contains(out, "[INFO] 기본 옵션") && len(out) > 24,
		"nil 옵션 보정", "기본 시각 형식과 Info 레벨이 적용됨",
		fmt.Sprintf("출력=%q", out))

	// 레벨을 지정하지 않으면 Info가 기본이다.
	buf.Reset()
	h = newHandler(&buf, &handlerOptions{})
	l := slog.New(h)
	l.Debug("걸러져야 함")
	l.Info("남아야 함")
	out = buf.String()
	r.Check(!strings.Contains(out, "걸러져야 함") && strings.Contains(out, "남아야 함"),
		"Level 미지정 보정", "Info가 기본으로 적용되어 Debug는 차단됨",
		fmt.Sprintf("출력=%q", out))

	// 빈 인자는 새 핸들러를 만들지 않고 자기 자신을 돌려준다.
	base := newHandler(&buf, &handlerOptions{Level: slog.LevelInfo})
	r.Check(base.WithAttrs(nil) == slog.Handler(base), "WithAttrs(빈 슬라이스)",
		"같은 핸들러를 반환 (불필요한 복제 없음)", "새 핸들러를 만듦")
	r.Check(base.WithGroup("") == slog.Handler(base), "WithGroup(빈 이름)",
		"같은 핸들러를 반환 (불필요한 복제 없음)", "새 핸들러를 만듦")

	// 멀티바이트 문자: 인용이 필요 없는 경우와 필요한 경우
	buf.Reset()
	slog.New(newHandler(&buf, &handlerOptions{Level: slog.LevelInfo})).
		Info("한글 메시지", "이름", "홍길동", "설명", "공백 있는 한글")
	out = strings.TrimSuffix(buf.String(), "\n")
	r.Check(!strings.Contains(out, "\n"), "멀티바이트 값 한 줄 유지",
		"한글 키·값이 줄을 깨뜨리지 않음", fmt.Sprintf("여러 줄로 분리됨: %q", out))
	r.Check(strings.Contains(out, "이름=홍길동"), "멀티바이트 인용 불필요",
		"공백 없는 한글은 인용하지 않음", fmt.Sprintf("출력=%q", out))
	r.Check(strings.Contains(out, `설명="공백 있는 한글"`), "멀티바이트 인용 필요",
		"공백 있는 한글은 인용됨", fmt.Sprintf("출력=%q", out))

	// 잘못된 UTF-8과 출력 불가 문자는 인용 대상이다.
	buf.Reset()
	slog.New(newHandler(&buf, &handlerOptions{Level: slog.LevelInfo})).
		Info("이상값", "bad", string([]byte{0xff, 0xfe}), "ctrl", "a\x01b")
	out = strings.TrimSuffix(buf.String(), "\n")
	r.Check(!strings.Contains(out, "\n") && strings.Contains(out, `bad="`) && strings.Contains(out, `ctrl="`),
		"잘못된 UTF-8/제어 문자", "인용되어 한 줄 유지",
		fmt.Sprintf("출력=%q", out))
}
