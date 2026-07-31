package logger

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/team-core-1/util/internal/testreport"
)

func TestMain(m *testing.M) { testreport.Main(m, "logger") }

// initTemp는 임시 디렉터리에 로그 파일을 두고 로거를 초기화한다.
// 반환값은 로그 파일 경로다.
func initTemp(t *testing.T, level LogLevel) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "app.log")
	if err := Init(Config{Path: path, Level: level}); err != nil {
		t.Fatalf("Init 실패: %v", err)
	}
	t.Cleanup(func() { _ = Close() })
	return path
}

// readLog는 기록된 내용을 읽는다. Close로 파일 디스크립터를 정리한 뒤 읽는다.
func readLog(t *testing.T, path string) string {
	t.Helper()
	if err := Close(); err != nil {
		t.Fatalf("Close 실패: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("로그 파일 읽기 실패: %v", err)
	}
	return string(b)
}

// ---------------------------------------------------------------------------
// T1. 초기화와 종료
// ---------------------------------------------------------------------------

func TestLogger_InitAndClose(t *testing.T) {
	r := testreport.New(t, "Init의 사전 검증과 Close의 반복 호출 안전성 확인", "임시 디렉터리 사용")
	defer r.Done()

	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")

	err := Init(Config{Path: path, Level: LogLevelInfo})
	r.CheckErr(err, nil, "정상 Init", "성공")

	// Init은 기록 가능 여부를 미리 확인하므로 파일이 만들어진다.
	_, statErr := os.Stat(path)
	r.Check(statErr == nil, "Init의 사전 파일 확인", "로그 파일이 즉시 생성됨",
		fmt.Sprintf("stat=%v", statErr))

	// 하위 디렉터리가 없어도 만들어 준다.
	nested := filepath.Join(dir, "a", "b", "nested.log")
	r.CheckErr(Init(Config{Path: nested, Level: LogLevelInfo}), nil,
		"하위 디렉터리 자동 생성", "중간 경로를 만들고 성공")

	r.CheckErr(Close(), nil, "Close", "성공")
	r.CheckErr(Close(), nil, "중복 Close", "no-op으로 성공")

	// Close 이후에도 기록하면 파일이 자동 재오픈되어 유실되지 않는다.
	Info("Close 이후 기록")
	_ = Close()
	b, _ := os.ReadFile(nested)
	r.Check(strings.Contains(string(b), "Close 이후 기록"), "Close 이후 기록",
		"파일이 재오픈되어 기록됨", "기록이 유실됨")
}

// ---------------------------------------------------------------------------
// T2. 기본 로깅
// ---------------------------------------------------------------------------

func TestLogger_BasicLogging(t *testing.T) {
	r := testreport.New(t, "레벨별 기록과 속성 출력 형식 확인", "Level Debug, 임시 파일")
	defer r.Done()

	path := initTemp(t, LogLevelDebug)

	Debug("디버그 메시지", "k", "v")
	Info("정보 메시지", "count", 3)
	Warn("경고 메시지")
	Error("오류 메시지", "err", "something")

	content := readLog(t, path)
	lines := strings.Split(strings.TrimSpace(content), "\n")

	r.Check(len(lines) == 4, "4개 레벨 기록", "각 레벨이 한 줄씩 기록됨",
		fmt.Sprintf("%d줄 기록됨", len(lines)))

	for _, lv := range []string{"DEBUG", "INFO", "WARN", "ERROR"} {
		r.Check(strings.Contains(content, "["+lv+"]"), "레벨 표기 "+lv,
			fmt.Sprintf("[%s] 형식으로 출력", lv), "표기 누락")
	}

	r.Check(strings.Contains(content, "k=v") && strings.Contains(content, "count=3"),
		"속성 출력", "key=value 형식으로 출력됨", "속성 누락")

	// 시각은 설정한 형식으로 앞에 붙는다.
	r.Check(len(lines[0]) > 23 && lines[0][4] == '-' && lines[0][10] == 'T',
		"시각 형식", "2006-01-02T15:04:05.000 형식이 앞에 붙음",
		fmt.Sprintf("첫 줄: %.30s", lines[0]))
}

// ---------------------------------------------------------------------------
// T3. 설정 검증
// ---------------------------------------------------------------------------

func TestLogger_ConfigValidation(t *testing.T) {
	r := testreport.New(t, "잘못된 설정이 사전에 걸러지고 기존 로거가 보존되는지", "빈 경로 / 디렉터리 경로 / 음수 값")
	defer r.Done()

	r.CheckErr(Init(Config{Level: LogLevelInfo}), ErrEmptyPath, "빈 Path", "ErrEmptyPath")

	dir := t.TempDir()

	// 정상 로거를 띄운 뒤, 잘못된 Init이 이를 망가뜨리지 않아야 한다.
	good := filepath.Join(dir, "good.log")
	if err := Init(Config{Path: good, Level: LogLevelInfo}); err != nil {
		t.Fatalf("Init 실패: %v", err)
	}
	Info("첫 로그")

	blocked := filepath.Join(dir, "blocked.log")
	if err := os.MkdirAll(blocked, 0755); err != nil {
		t.Fatalf("디렉터리 생성 실패: %v", err)
	}
	err := Init(Config{Path: blocked, Level: LogLevelInfo})
	r.Check(err != nil, "쓸 수 없는 경로", "파일을 열어 보고 실패를 즉시 반환",
		"성공으로 보고됨 (기록 실패가 무통보로 묻힘)")

	Info("두 번째 로그")
	content := readLog(t, good)
	r.Check(strings.Contains(content, "첫 로그") && strings.Contains(content, "두 번째 로그"),
		"Init 실패 시 기존 로거 보존", "실패 전후 기록이 모두 남음",
		"기존 로거가 교체되거나 끊김")

	// 음수 설정은 기본값으로 보정된다. 보정하지 않으면 로그가 전량 폐기된다.
	neg := filepath.Join(dir, "neg.log")
	r.CheckErr(Init(Config{Path: neg, MaxSize: -1, MaxBackups: -1, MaxAge: -1, Level: LogLevelInfo}),
		nil, "음수 설정 Init", "기본값으로 보정되어 성공")
	Info("음수 설정에서도 기록")
	negContent := readLog(t, neg)
	r.Check(strings.Contains(negContent, "음수 설정에서도 기록"), "음수 설정 보정 결과",
		"MaxSize 보정으로 기록이 정상 수행됨", "기록이 폐기됨")
}

// ---------------------------------------------------------------------------
// T4. 로그 레벨
// ---------------------------------------------------------------------------

func TestLogger_LogLevel(t *testing.T) {
	r := testreport.New(t, "레벨 필터링과 런타임 레벨 변경 확인", "Info로 시작해 Debug로 변경")
	defer r.Done()

	path := initTemp(t, LogLevelInfo)

	r.Check(GetLogLevel() == LogLevelInfo, "초기 레벨", "Init에서 지정한 Info",
		fmt.Sprintf("%v", GetLogLevel()))

	Debug("걸러져야 하는 디버그")
	Info("남아야 하는 정보")

	SetLogLevel(LogLevelDebug)
	r.Check(GetLogLevel() == LogLevelDebug, "SetLogLevel 반영", "Debug로 변경됨",
		fmt.Sprintf("%v", GetLogLevel()))
	Debug("이제 남아야 하는 디버그")

	SetLogLevel(LogLevelError)
	Warn("다시 걸러져야 하는 경고")
	Error("남아야 하는 오류")

	content := readLog(t, path)
	r.Check(!strings.Contains(content, "걸러져야 하는 디버그"), "레벨 미만 차단",
		"Info 설정에서 Debug가 기록되지 않음", "차단되지 않음")
	r.Check(strings.Contains(content, "남아야 하는 정보"), "레벨 이상 기록",
		"Info가 기록됨", "기록 누락")
	r.Check(strings.Contains(content, "이제 남아야 하는 디버그"), "레벨 하향 반영",
		"Debug로 낮춘 뒤 Debug가 기록됨", "반영되지 않음")
	r.Check(!strings.Contains(content, "다시 걸러져야 하는 경고") &&
		strings.Contains(content, "남아야 하는 오류"), "레벨 상향 반영",
		"Error로 올린 뒤 Warn은 차단되고 Error만 기록됨", "반영되지 않음")
}

// ---------------------------------------------------------------------------
// T5. Context 계열 함수
// ---------------------------------------------------------------------------

func TestLogger_ContextVariants(t *testing.T) {
	r := testreport.New(t, "XxxContext 계열이 레벨 필터를 거쳐 정상 기록되는지", "Level Debug, context.Background 사용")
	defer r.Done()

	path := initTemp(t, LogLevelDebug)
	ctx := context.Background()

	DebugContext(ctx, "컨텍스트 디버그")
	InfoContext(ctx, "컨텍스트 정보", "user", "alice")
	WarnContext(ctx, "컨텍스트 경고")
	ErrorContext(ctx, "컨텍스트 오류")

	content := readLog(t, path)
	all := true
	for _, msg := range []string{"컨텍스트 디버그", "컨텍스트 정보", "컨텍스트 경고", "컨텍스트 오류"} {
		if !strings.Contains(content, msg) {
			all = false
		}
	}
	r.Check(all, "4개 Context 함수", "모두 기록됨", "일부 기록 누락")
	r.Check(strings.Contains(content, "user=alice"), "Context 함수의 속성",
		"속성이 함께 기록됨", "속성 누락")

	// 레벨 필터는 Context 계열에도 동일하게 적용된다.
	path2 := initTemp(t, LogLevelWarn)
	InfoContext(ctx, "걸러져야 하는 컨텍스트 정보")
	WarnContext(ctx, "남아야 하는 컨텍스트 경고")
	content2 := readLog(t, path2)
	r.Check(!strings.Contains(content2, "걸러져야 하는") && strings.Contains(content2, "남아야 하는"),
		"Context 함수의 레벨 필터", "Warn 설정에서 Info가 차단됨", "필터가 적용되지 않음")
}

// ---------------------------------------------------------------------------
// T6. 동시성 안전성 및 처리 성능
//
// 로거는 패키지 전역 상태(기본 로거, 레벨, 파일 디스크립터)를 다루므로
// 로깅 중 Init/Close/레벨 변경이 겹쳐도 안전해야 한다.
// 성능 수치는 머신 사양과 디스크에 좌우되므로 단언하지 않는다.
// ---------------------------------------------------------------------------

func TestLogger_Concurrency(t *testing.T) {
	r := testreport.New(t,
		"로깅 중 Init/Close/레벨 변경이 겹쳐도 안전한지와 처리 성능 확인",
		"기록 8 고루틴 + Init/Close/레벨 변경 동시 수행")
	defer r.Done()

	dir := t.TempDir()
	if err := Init(Config{Path: filepath.Join(dir, "base.log"), Level: LogLevelInfo}); err != nil {
		t.Fatalf("Init 실패: %v", err)
	}

	var logged atomic.Uint64
	var wg sync.WaitGroup
	stop := make(chan struct{})
	begin := time.Now()

	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				Info("동시 로깅", "goroutine", i)
				logged.Add(1)
			}
		}(i)
	}
	// 재초기화, 종료, 레벨 변경을 동시에 수행한다.
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_ = Init(Config{Path: filepath.Join(dir, fmt.Sprintf("re%d.log", i)), Level: LogLevelInfo})
				_ = Close()
			}
		}(i)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 200; j++ {
			SetLogLevel(LogLevelDebug)
			_ = GetLogLevel()
			SetLogLevel(LogLevelInfo)
		}
	}()

	// 재초기화/레벨 변경 고루틴이 끝나면 기록을 멈춘다.
	time.Sleep(50 * time.Millisecond)
	close(stop)

	finished := make(chan struct{})
	go func() { wg.Wait(); close(finished) }()
	select {
	case <-finished:
		r.Check(true, "동시 실행 완주",
			fmt.Sprintf("패닉·데드락 없이 %v 내 완료", time.Since(begin).Round(time.Millisecond)), "")
	case <-time.After(60 * time.Second):
		r.Check(false, "동시 실행 완주", "", "60초 내 완료되지 않음 (데드락 의심)")
		return
	}

	r.Check(logged.Load() > 0, "동시 기록 수행", fmt.Sprintf("%d건 기록됨", logged.Load()),
		"한 건도 기록되지 않음")
	r.CheckErr(Close(), nil, "정리 후 Close", "성공")

	// 성능 측정: 파일 기록까지 포함한 단일 고루틴 처리량
	perfPath := filepath.Join(dir, "perf.log")
	if err := Init(Config{Path: perfPath, Level: LogLevelInfo}); err != nil {
		t.Fatalf("Init 실패: %v", err)
	}
	defer Close()

	const n = 20000
	for i := 0; i < n; i++ { // 워밍업
		Info("warmup", "i", i)
	}
	start := time.Now()
	for i := 0; i < n; i++ {
		Info("benchmark message", "user_id", 12345, "status", "ok")
	}
	writeDur := time.Since(start)

	// 레벨로 걸러지는 경로는 포맷팅도 하지 않는다.
	SetLogLevel(LogLevelError)
	start = time.Now()
	for i := 0; i < n; i++ {
		Info("filtered out", "user_id", 12345)
	}
	filteredDur := time.Since(start)
	SetLogLevel(LogLevelInfo)

	r.Note("기록 (파일 I/O 포함)  평균 %v   %.0f건/초",
		(writeDur / n).Round(time.Nanosecond), float64(n)/writeDur.Seconds())
	r.Note("레벨 필터로 차단       평균 %v", (filteredDur / n).Round(time.Nanosecond))
	r.Note("(-race 실행 시 위 수치는 수 배 부풀려짐)")
}
