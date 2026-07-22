package logger

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/lmittmann/tint"
	"gopkg.in/natefinch/lumberjack.v2"
)

type LogLevel = slog.Level

const (
	LogLevelDebug = LogLevel(slog.LevelDebug)
	LogLevelInfo  = LogLevel(slog.LevelInfo)
	LogLevelWarn  = LogLevel(slog.LevelWarn)
	LogLevelError = LogLevel(slog.LevelError)
)

type Config struct {
	Path       string   `json:"path" yaml:"path"`
	MaxSize    int      `json:"max_size" yaml:"max_size"`
	MaxBackups int      `json:"max_backups" yaml:"max_backups"`
	MaxAge     int      `json:"max_age" yaml:"max_age"`
	Level      LogLevel `json:"level" yaml:"level"`
}

var (
	levelVar  = new(slog.LevelVar)
	logCloser io.Closer
)

func Init(cfg Config) error {
	if logCloser != nil {
		_ = logCloser.Close()
		logCloser = nil
	}

	dir := filepath.Dir(cfg.Path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	logWriter := &lumberjack.Logger{
		Filename:   cfg.Path,
		MaxSize:    cfg.MaxSize,
		MaxBackups: cfg.MaxBackups,
		MaxAge:     cfg.MaxAge,
		Compress:   true,
		LocalTime:  true,
	}

	logCloser = logWriter
	levelVar.Set(cfg.Level)

	handler := tint.NewHandler(logWriter, &tint.Options{
		Level:      levelVar,
		TimeFormat: "2006-01-02T15:04:05.000",
		NoColor:    true,
	})

	/*
		handler := slog.NewTextHandler(logWriter,
			&slog.HandlerOptions{
				Level: levelVar,
				ReplaceAttr: func(groups []string, attr slog.Attr) slog.Attr {
					switch attr.Key {
					case slog.TimeKey:
						return slog.Attr{
							Key:   attr.Key,
							Value: slog.StringValue(attr.Value.Time().Format("2006-01-02T15:04:05.000")),
						}
					case slog.LevelKey:
						return slog.Attr{
							Key: attr.Key,
							Value: slog.StringValue(func() string {
								switch attr.Value.String() {
								case "DEBUG":
									return "[DEBUG]"
								case "INFO":
									return "[_INFO]"
								case "WARN":
									return "[_WARN]"
								case "ERROR":
									return "[ERROR]"
								default:
									return attr.Value.String()
								}
							}()),
						}
					default:
						return attr
					}
				},
			},
		)
	*/

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
//	    if err := logger.Init(logger.Config{
//	        Path:       "app.log",
//	        MaxSize:    100,
//	        MaxBackups: 100,
//	        MaxAge:     30,
//	        Level:      logger.LogLevelInfo,
//	    }); err != nil {
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

func SetLogLevel(level LogLevel) {
	levelVar.Set(level)
}

func GetLogLevel() LogLevel {
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
