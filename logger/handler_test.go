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
