package logger

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/natefinch/lumberjack.v2"
)

type ErrorType string

func (e ErrorType) Error() string {
	return string(e)
}

const (
	ErrEmptyPath = ErrorType("logger: config path is empty")
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
	mu        sync.Mutex
	levelVar  = new(slog.LevelVar)
	logCloser io.Closer
)

// Init은 로그 파일 기반 핸들러를 구성하고 slog 기본 로거로 등록합니다.
// 이미 초기화된 상태에서 다시 호출하면 기존 로그 파일을 닫고 새 설정으로 교체합니다.
//
// cfg.Path가 비어 있으면 ErrEmptyPath를 반환합니다.
// (빈 경로를 그대로 넘기면 로그가 의도치 않게 임시 디렉터리로 기록되므로 사전에 차단합니다.)
// MaxSize/MaxBackups/MaxAge가 0 이하이면 각각 100(MB)/100(개)/30(일)이 적용됩니다.
//
// Init은 설정 검증을 마친 뒤에야 기존 로거를 교체합니다.
// 따라서 Init이 에러를 반환하면 직전까지 사용하던 로거가 그대로 유지되며,
// 잘못된 설정 때문에 로그가 끊기는 일은 없습니다.
func Init(cfg Config) error {
	if cfg.Path == "" {
		return ErrEmptyPath
	}

	if cfg.MaxSize <= 0 {
		cfg.MaxSize = 100
	}
	if cfg.MaxBackups <= 0 {
		cfg.MaxBackups = 100
	}
	if cfg.MaxAge <= 0 {
		cfg.MaxAge = 30
	}

	dir := filepath.Dir(cfg.Path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	// 로그 파일을 실제로 열어 기록 가능한지 미리 확인한다.
	// lumberjack은 첫 기록 시점에야 파일을 열고, 그때 발생한 오류는
	// slog가 핸들러의 반환 에러를 버리기 때문에 어디에도 드러나지 않는다.
	// 여기서 확인하지 않으면 권한 부족, 경로가 디렉터리인 경우 등이
	// 아무 신호 없이 로그 전량 유실로 이어진다.
	// 파일 모드 0600은 lumberjack이 새 파일을 만들 때 쓰는 값과 동일하다.
	f, err := os.OpenFile(cfg.Path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	mu.Lock()
	defer mu.Unlock()

	if logCloser != nil {
		_ = logCloser.Close()
		logCloser = nil
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

	handler := newHandler(logWriter, &handlerOptions{
		Level:      levelVar,
		TimeFormat: "2006-01-02T15:04:05.000",
	})

	slog.SetDefault(slog.New(handler))

	return nil
}

// Close는 열려있는 로그 파일 디스크립터를 닫습니다.
//
// [동작 범위: fsync를 수행하지 않음]
//   - 각 로그 레코드는 기록 시점에 파일로 곧바로 write되며, 애플리케이션 레벨 버퍼링이 없습니다.
//     따라서 Close가 따로 비워낼 로그 버퍼는 존재하지 않습니다.
//   - Close는 파일 디스크립터만 해제하며 fsync를 호출하지 않습니다.
//     이미 write된 로그는 OS 페이지 캐시에 있으므로 프로세스가 비정상 종료되어도 보존되지만,
//     전원 차단 등 OS/머신 레벨 장애에 대한 내구성까지 보장하지는 않습니다.
//   - Close 이후에 로그를 다시 기록하면 파일이 자동으로 재오픈되므로 로그가 유실되지는 않습니다.
//     다만 Init을 다시 호출하기 전까지 이후의 Close 호출은 no-op이 됩니다.
//
// [사용 가이드: 종료 시 파일 디스크립터 정리]
// 프로그램 엔트리 포인트(main.go)에서 logger.Init 호출 직후 `defer logger.Close()`를 등록하면,
// 정상 종료는 물론 메인 고루틴 panic으로 인한 종료 시에도 파일 디스크립터가 정리됩니다.
// panic 시점까지 기록된 로그는 Close 호출 여부와 무관하게 이미 파일에 write되어 있습니다:
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
// [사용 가이드: 서브 고루틴 패닉 대비]
// 서브 고루틴에서 발생한 panic은 메인 고루틴의 defer를 거치지 않고 프로세스를 즉시 종료시키므로,
// 원인을 남기려면 고루틴 내부에서 직접 로그를 기록해야 합니다.
// 이때 별도의 Close(동기화) 호출은 필요하지 않습니다. Error 호출이 반환된 시점에 이미 파일에 기록됩니다:
//
//	go func() {
//	    defer func() {
//	        if r := recover(); r != nil {
//	            logger.Error("goroutine panicked", "error", r) // 이 시점에 파일 기록 완료
//	            panic(r)
//	        }
//	    }()
//	    // 비즈니스 로직 실행
//	}()
func Close() error {
	mu.Lock()
	defer mu.Unlock()

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
