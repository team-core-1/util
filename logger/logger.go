package logger

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"gopkg.in/natefinch/lumberjack.v2"
)

const (
	LogLevelDebug = slog.LevelDebug
	LogLevelInfo  = slog.LevelInfo
	LogLevelWarn  = slog.LevelWarn
	LogLevelError = slog.LevelError
)

var (
	levelVar  = new(slog.LevelVar)
	logCloser io.Closer
)

func Init(path string, level slog.Level) error {
	if logCloser != nil {
		_ = logCloser.Close()
		logCloser = nil
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	logWriter := &lumberjack.Logger{
		Filename:   path,
		MaxSize:    100,
		MaxBackups: 100,
		MaxAge:     30,
		Compress:   true,
		LocalTime:  true, // 백업 로테이션 파일명에도 로컬 타임스탬프(밀리초 포함)가 적용되도록 보장
	}

	logCloser = logWriter
	levelVar.Set(level)

	handler := slog.NewTextHandler(logWriter,
		&slog.HandlerOptions{
			Level: levelVar,
			ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
				switch a.Key {
				case slog.TimeKey:
					localTimeStr := a.Value.Time().Format("2006-01-02T15:04:05.000")
					return slog.Attr{
						Key:   a.Key,
						Value: slog.StringValue(localTimeStr),
					}
				default:
					return a
				}
			},
		},
	)

	slog.SetDefault(slog.New(handler))

	return nil
}

// Close는 열려있는 로그 파일 디스크립터 리소스를 닫고, 메모리(OS 페이지 캐시)의 로그 버퍼를 디스크에 강제 플러시합니다.
//
// [사용 가이드 1: 메인 고루틴 패닉 대비]
// 프로그램 엔트리 포인트(main.go) 시작 시 logger.Init 호출 직후 `defer logger.Close()`를 즉시 등록하면,
// 메인 고루틴에서 panic이 발생해 프로세스가 강제 종료될 때도 마지막 로그가 누락 없이 디스크에 기록됩니다:
//
//	func main() {
//	    if err := logger.Init("app.log", logger.LevelInfo); err != nil {
//	        panic(err)
//	    }
//	    defer logger.Close()
//
//	    // 비즈니스 로직 실행
//	}
//
// [사용 가이드 2: 서브 고루틴 패닉 대비]
// 서브 고루틴에서 발생하는 panic은 메인 고루틴의 defer를 거치지 않고 프로세스를 즉시 종료시킵니다.
// 따라서 신규 고루틴 실행 시 아래와 같은 패닉 복구 및 즉시 동기화 패턴 사용을 권장합니다:
//
//	go func() {
//	    defer func() {
//	        if r := recover(); r != nil {
//	            logger.Error("goroutine panicked", "error", r)
//	            logger.Close() // 즉시 디스크 동기화(Flush) 보장
//	            panic(r)
//	        }
//	    }()
//	    // 비즈니스 로직 실행
//	}()
func Close() error {
	if logCloser != nil {
		err := logCloser.Close()
		logCloser = nil
		return err
	}

	return nil
}

func SetLogLevel(level slog.Level) {
	levelVar.Set(level)
}

func GetLogLevel() slog.Level {
	return levelVar.Level()
}

func Debug(msg string, args ...any)                             { slog.Debug(msg, args...) }
func Info(msg string, args ...any)                              { slog.Info(msg, args...) }
func Warn(msg string, args ...any)                              { slog.Warn(msg, args...) }
func Error(msg string, args ...any)                             { slog.Error(msg, args...) }
func DebugContext(ctx context.Context, msg string, args ...any) { slog.DebugContext(ctx, msg, args...) }
func InfoContext(ctx context.Context, msg string, args ...any)  { slog.InfoContext(ctx, msg, args...) }
func WarnContext(ctx context.Context, msg string, args ...any)  { slog.WarnContext(ctx, msg, args...) }
func ErrorContext(ctx context.Context, msg string, args ...any) { slog.ErrorContext(ctx, msg, args...) }
