package logger

import (
	"context"
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
