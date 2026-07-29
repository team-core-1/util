package logger

import (
	"bytes"
	"log/slog"
	"strconv"
	"strings"
	"testing"
	"testing/slogtest"
)

// splitFields는 로그 한 줄을 필드 단위로 나눕니다.
// 큰따옴표로 감싼 구간의 공백은 분리하지 않습니다.
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

// parseLogLine은 "TIME [LEVEL] MSG k=v a.b=v ..." 형식의 한 줄을
// slogtest가 요구하는 map으로 변환합니다.
// 점(.)으로 구분된 키는 중첩 map으로 복원합니다.
func parseLogLine(line string) map[string]any {
	m := map[string]any{}

	fields := splitFields(line)
	if len(fields) == 0 {
		return m
	}

	i := 0
	// 시각은 생략될 수 있음 (Record.Time이 0값인 경우)
	if !strings.HasPrefix(fields[i], "[") {
		m[slog.TimeKey] = fields[i]
		i++
	}
	if i < len(fields) && strings.HasPrefix(fields[i], "[") {
		m[slog.LevelKey] = strings.Trim(fields[i], "[]")
		i++
	}

	// '='가 나오기 전까지가 메시지
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

		// a.b.c=v -> {"a":{"b":{"c":"v"}}}
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

// TestHandler_SlogConformance는 표준 라이브러리의 적합성 검사(testing/slogtest)로
// 핸들러가 slog.Handler 규약을 지키는지 검증합니다.
//
// 검사 항목에는 WithAttrs/WithGroup의 한정 범위, 빈 그룹 생략,
// LogValuer의 Resolve 호출, 제로값 Attr 무시, 제로값 시각 생략 등이 포함됩니다.
func TestHandler_SlogConformance(t *testing.T) {
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

	t.Logf("==================================================")
	t.Logf(" [시험 목적 및 조건]")
	t.Logf("  - 시험 목적 : testing/slogtest 표준 적합성 검사를 통한 slog.Handler 규약 준수 검증")
	t.Logf("  - 시험 조건 : Level Debug, 메모리 버퍼 출력")
	t.Logf("--------------------------------------------------")

	if err := slogtest.TestHandler(h, results); err != nil {
		t.Errorf("slog 규약 위반:\n%v", err)
	}

	t.Logf(" [시험 결과] : 정상 (표준 적합성 검사 통과)")
	t.Logf("==================================================")
}

// TestHandler_Quoting은 구분자나 제어 문자가 포함된 값/키/메시지가
// 로그 한 줄의 구조를 깨뜨리지 않도록 인용되는지 검증합니다.
//
// 특히 줄바꿈이 그대로 출력되면 로그 1건이 여러 줄로 쪼개져,
// 줄 단위 로그 수집기가 가짜 레코드를 정상 레코드로 인식하게 됩니다(로그 위조).
func TestHandler_Quoting(t *testing.T) {
	// 가짜 로그 레코드를 주입하려는 입력
	forged := "normal\n2026-01-01T00:00:00.000 [ERROR] granted user=attacker"

	cases := []struct {
		name string
		log  func(l *slog.Logger)
		want string // 메시지 이후에 기대되는 문자열
	}{
		{
			name: "값에 줄바꿈",
			log:  func(l *slog.Logger) { l.Info("m", "input", forged) },
			want: `input="normal\n2026-01-01T00:00:00.000 [ERROR] granted user=attacker"`,
		},
		{
			name: "값에 공백",
			log:  func(l *slog.Logger) { l.Info("m", "note", "hello world") },
			want: `note="hello world"`,
		},
		{
			name: "값에 등호",
			log:  func(l *slog.Logger) { l.Info("m", "expr", "a=b") },
			want: `expr="a=b"`,
		},
		{
			name: "값에 인용부호",
			log:  func(l *slog.Logger) { l.Info("m", "q", `he said "hi"`) },
			want: `q="he said \"hi\""`,
		},
		{
			name: "빈 값",
			log:  func(l *slog.Logger) { l.Info("m", "empty", "") },
			want: `empty=""`,
		},
		{
			name: "키에 공백",
			log:  func(l *slog.Logger) { l.Info("m", "my key", "v") },
			want: `"my key"=v`,
		},
		{
			name: "그룹명에 공백",
			log:  func(l *slog.Logger) { l.WithGroup("g p").Info("m", "k", "v") },
			want: `"g p.k"=v`,
		},
		{
			name: "인용이 불필요한 정상 값",
			log:  func(l *slog.Logger) { l.Info("m", "user", "alice", "count", 3) },
			want: `user=alice count=3`,
		},
	}

	t.Logf("==================================================")
	t.Logf(" [시험 목적 및 조건]")
	t.Logf("  - 시험 목적 : 구분자/제어 문자 포함 시 인용 처리 및 로그 위조 차단 검증")
	t.Logf("  - 시험 조건 : 값/키/그룹명 경로별 %d개 케이스", len(cases))
	t.Logf("--------------------------------------------------")

	for _, c := range cases {
		var buf bytes.Buffer
		l := slog.New(newHandler(&buf, &handlerOptions{Level: slog.LevelDebug}))
		c.log(l)

		out := strings.TrimSuffix(buf.String(), "\n")

		// 어떤 입력이든 로그는 항상 한 줄이어야 함
		if strings.Contains(out, "\n") {
			t.Errorf("%s: 로그가 여러 줄로 분리됨 (로그 위조 가능)\n%s", c.name, out)
			continue
		}

		idx := strings.Index(out, "] ")
		if idx < 0 {
			t.Errorf("%s: 예상치 못한 출력 형식: %q", c.name, out)
			continue
		}
		got := out[idx+2:]

		if !strings.HasSuffix(got, c.want) {
			t.Errorf("%s: 인용 결과 불일치\n  기대(접미사): %s\n  실제        : %s", c.name, c.want, got)
			continue
		}
		t.Logf("  - %-22s : %s", c.name, got)
	}

	// 메시지 경로도 줄 구조를 깨뜨리지 않아야 함
	var buf bytes.Buffer
	l := slog.New(newHandler(&buf, &handlerOptions{Level: slog.LevelDebug}))
	l.Info(forged)
	if strings.Contains(strings.TrimSuffix(buf.String(), "\n"), "\n") {
		t.Errorf("메시지에 줄바꿈: 로그가 여러 줄로 분리됨 (로그 위조 가능)\n%s", buf.String())
	} else {
		t.Logf("  - %-22s : %s", "메시지에 줄바꿈", strings.TrimSuffix(buf.String(), "\n"))
	}

	// 메시지의 공백은 가독성을 위해 인용하지 않아야 함
	buf.Reset()
	l = slog.New(newHandler(&buf, &handlerOptions{Level: slog.LevelDebug}))
	l.Info("hello world")
	if !strings.Contains(buf.String(), "] hello world") {
		t.Errorf("공백만 있는 메시지가 불필요하게 인용됨: %q", buf.String())
	}

	t.Logf(" [시험 결과] : 정상 (모든 주입 경로에서 단일 줄 유지)")
	t.Logf("==================================================")
}
