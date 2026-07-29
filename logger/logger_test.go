package logger

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLogger(t *testing.T) {
	// 임시 디렉터리에 테스트 로그 파일 지정
	tempDir, err := os.MkdirTemp("", "logger_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	logPath := filepath.Join(tempDir, "test.log")

	// 1. Init 테스트 (LogLevelInfo로 설정)
	err = Init(Config{
		Path:       logPath,
		MaxSize:    100,
		MaxBackups: 100,
		MaxAge:     30,
		Level:      LogLevelInfo,
	})
	if err != nil {
		t.Fatalf("failed to initialize logger: %v", err)
	}

	// 2. 로그 작성 테스트
	Info("info message test", "key1", "val1")
	Debug("debug message test (should not be logged)") // Info 레벨이므로 무시되어야 함

	// 3. 로그 레벨 변경 테스트
	SetLogLevel(LogLevelDebug)
	if GetLogLevel() != LogLevelDebug {
		t.Errorf("expected log level to be LevelDebug, got %v", GetLogLevel())
	}
	Debug("debug message test (should be logged now)")

	// 4. 컨텍스트 활용 테스트
	ctx := context.WithValue(context.Background(), "trace_id", "12345")
	InfoContext(ctx, "info message with context", "user_id", "99")

	// 5. 리소스 Close 테스트
	err = Close()
	if err != nil {
		t.Fatalf("failed to close logger: %v", err)
	}

	// 6. 파일에 써진 로그 내용 검증
	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read log file: %v", err)
	}

	logContent := string(logBytes)

	// info message test가 포함되어 있어야 함
	if !strings.Contains(logContent, "info message test") {
		t.Errorf("expected log to contain 'info message test', but it didn't. Content: %s", logContent)
	}
	if !strings.Contains(logContent, "key1=val1") {
		t.Errorf("expected log to contain 'key1=val1', but it didn't. Content: %s", logContent)
	}

	// 첫 번째 debug message는 Info 레벨일 때 호출되었으므로 로그에 없어야 함
	if strings.Contains(logContent, "should not be logged") {
		t.Errorf("log contains debug message that shouldn't be logged. Content: %s", logContent)
	}

	// 두 번째 debug message는 레벨 변경 후 호출되었으므로 로그에 있어야 함
	if !strings.Contains(logContent, "should be logged now") {
		t.Errorf("expected log to contain 'should be logged now', but it didn't. Content: %s", logContent)
	}

	// Context 로깅 동작 여부 확인
	if !strings.Contains(logContent, "info message with context") {
		t.Errorf("expected log to contain 'info message with context', but it didn't. Content: %s", logContent)
	}

	t.Logf("Success! Log file content:\n%s", logContent)
}

// Init은 빈 Path를 거부해야 함 (빈 값이면 로그가 임시 디렉터리로 새어나감)
func TestLogger_InitEmptyPath(t *testing.T) {
	if err := Init(Config{Level: LogLevelInfo}); err != ErrEmptyPath {
		t.Errorf("빈 Path로 Init: ErrEmptyPath 기대, 실제 %v", err)
	}
}

// slog 규약 검증: WithGroup은 "그 이후에 추가된" 속성만 그룹으로 한정해야 하며,
// WithGroup 이전에 등록된 속성은 그룹의 영향을 받지 않아야 함.
func TestLogger_GroupAndAttrs(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "group.log")
	if err := Init(Config{Path: logPath, Level: LogLevelInfo}); err != nil {
		t.Fatalf("failed to initialize logger: %v", err)
	}

	// case1: 그룹 이전 속성(before)은 한정되지 않고, 이후 속성(after)과 레코드 속성(inline)만 한정
	slog.Default().With("before", 1).WithGroup("g").With("after", 2).Info("case1", "inline", 3)
	// case2: 중첩 그룹 - x는 g1에서만, y는 g1.g2에서 한정
	slog.Default().WithGroup("g1").With("x", 1).WithGroup("g2").Info("case2", "y", 2)
	// case3: 속성 없는 그룹은 아무것도 출력하지 않음
	slog.Default().WithGroup("empty").Info("case3")
	// case4: 그룹 값 속성은 그룹명을 접두사로 펼침
	slog.Default().Info("case4", slog.Group("gr", "a", 1, "b", 2))
	// case5: 빈 그룹 값 속성은 생략
	slog.Default().Info("case5", slog.Group("emptygr"))

	if err := Close(); err != nil {
		t.Fatalf("failed to close logger: %v", err)
	}

	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read log file: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(logBytes)), "\n")

	// 메시지 이후의 속성 문자열만 추출
	attrsOf := func(msg string) (string, bool) {
		for _, line := range lines {
			if idx := strings.Index(line, msg); idx >= 0 {
				return strings.TrimSpace(line[idx+len(msg):]), true
			}
		}
		return "", false
	}

	cases := []struct {
		msg  string
		want string
	}{
		{"case1", "before=1 g.after=2 g.inline=3"},
		{"case2", "g1.x=1 g1.g2.y=2"},
		{"case3", ""},
		{"case4", "gr.a=1 gr.b=2"},
		{"case5", ""},
	}

	t.Logf("==================================================")
	t.Logf(" [시험 목적 및 조건]")
	t.Logf("  - 시험 목적 : WithAttrs/WithGroup의 slog 규약 준수 검증")
	t.Logf("  - 시험 조건 : 그룹 전/후 속성, 중첩 그룹, 빈 그룹, 그룹 값 속성")
	t.Logf("--------------------------------------------------")

	for _, c := range cases {
		got, ok := attrsOf(c.msg)
		if !ok {
			t.Errorf("%s: 로그 라인을 찾지 못함", c.msg)
			continue
		}
		if got != c.want {
			t.Errorf("%s: 속성 불일치\n  기대: %q\n  실제: %q", c.msg, c.want, got)
			continue
		}
		t.Logf("  - %s : %q", c.msg, got)
	}

	t.Logf(" [시험 결과] : 정상 (그룹 한정 범위가 slog 규약과 일치)")
	t.Logf("==================================================")
}

func BenchmarkLogger(b *testing.B) {
	tempDir, err := os.MkdirTemp("", "logger_bench")
	if err != nil {
		b.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	logPath := filepath.Join(tempDir, "bench.log")
	_ = Init(Config{
		Path:       logPath,
		MaxSize:    100,
		MaxBackups: 1,
		MaxAge:     1,
		Level:      LogLevelInfo,
	})
	defer Close()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			Info("benchmark info message", "user_id", 12345, "status", "ok")
		}
	})
}
