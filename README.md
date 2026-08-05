# util

Go 동시성 유틸리티 모음입니다. 해시맵, 인덱스 메모리풀, 파이프 큐, 타이머, 로거 5개 패키지로 구성되며
각각 독립적으로 import할 수 있습니다.

* Go 1.25.12
* 모듈 경로: `github.com/team-core-1/util`
* 4개 패키지(`hashmap`, `indexmempool`, `pipequeue`, `timer`)가 공통 미들웨어 구조를 공유합니다

## 패키지

| 패키지 | 용도 | 외부 의존 |
|---|---|---|
| [`hashmap`](hashmap/) | 용량 제한이 있는 동시성 맵 | 없음 |
| [`indexmempool`](indexmempool/) | 인덱스로 접근하는 고정 크기 메모리 풀 | 없음 |
| [`pipequeue`](pipequeue/) | 미들웨어를 거쳐 채널로 전달하는 단방향 큐 | 없음 |
| [`timer`](timer/) | 만료된 키를 채널로 받는 타이머 엔진 | `timingwheel`, `pipequeue` |
| [`logger`](logger/) | 파일 로테이션을 지원하는 `slog` 기반 로거 | `lumberjack` |

필요한 패키지만 import하면 그 패키지의 의존만 따라옵니다.
`hashmap`, `indexmempool`, `pipequeue`는 외부 의존이 없습니다.

5G SBI OpenAPI 스펙에서 Go 구조체를 생성하는 도구는 [`openapi/`](openapi/)에 별도로 있습니다.

## 빠른 시작

### hashmap

```go
hm, err := hashmap.New[string, int](1000)
if err != nil {
    return err
}
defer hm.Close()

if err := hm.Put("user1", 42); err != nil {
    return err // ErrFull, ErrDupKey
}
v, err := hm.Get("user1")
hm.Delete("user1")
```

### indexmempool

```go
type session struct{ ID string }

pool, _ := indexmempool.New[session](128)

idx, err := pool.Get() // ErrEmpty면 여유 슬롯 없음
if err != nil {
    return err
}
_ = pool.Access(idx, func(s *session) { s.ID = "abc" })
_ = pool.Put(idx) // 반납
```

### pipequeue

```go
q, _ := pipequeue.New[int](1024)
defer q.Close()

done := make(chan struct{})

go func() { // 소비자가 반드시 있어야 합니다
    for {
        select {
        case v, ok := <-q.C():
            if !ok {
                return // Close로 채널이 닫힘
            }
            process(v)
        case <-done:
            return
        }
    }
}()

if err := q.Put(1); err != nil {
    return err // ErrFull, ErrClosed
}
```

`select`에서 수신할 때는 **채널이 닫혔는지(`ok`)를 반드시 확인해야 합니다.**
닫힌 채널은 계속 준비 상태가 되므로, 확인하지 않으면 제로값을 무한히 받는 바쁜 대기에 빠집니다.
여러 큐를 한 `select`에서 다룬다면 닫힌 큐의 변수를 `nil`로 바꿔 해당 case를 비활성화하십시오.
`C()`는 nil 리시버에서 nil 채널을 반환하므로 이 방식이 그대로 동작합니다.

### timer

```go
tw := timingwheel.NewTimingWheel(10*time.Millisecond, 20)
tw.Start()
defer tw.Stop() // timingWheel 수명은 호출 측 책임입니다

eng, _ := timer.New[string](tw, 1000)
defer eng.Close()

done := make(chan struct{})

go func() { // 소비자가 반드시 있어야 합니다
    for {
        select {
        case key, ok := <-eng.C(): // 만료된 키
            if !ok {
                return // Close로 채널이 닫힘
            }
            onExpire(key)
        case <-done:
            return
        }
    }
}()

tm, err := eng.Set(30*time.Second, "session-1")
if err != nil {
    return err // ErrExpiredQueueFull, ErrClosed
}
_ = eng.Cancel(tm)
```

### logger

```go
if err := logger.Init(logger.Config{
    Path:       "log/app.log",
    MaxSize:    100, // MB
    MaxBackups: 100,
    MaxAge:     30, // 일
    Level:      logger.LogLevelInfo,
}); err != nil {
    panic(err)
}
defer logger.Close()

logger.Info("서버 시작", "port", 8080)
logger.SetLogLevel(logger.LogLevelDebug) // 런타임 변경
```

## 공통 개념: 미들웨어

`hashmap`, `indexmempool`, `pipequeue`, `timer`는 같은 구조의 미들웨어를 지원합니다.
`Use`로 등록한 핸들러가 연산 전후에 실행되며, `Context.Next()`로 후속 처리를 감쌉니다.

```go
q.Use(func(c *pipequeue.Context[int]) {
    start := time.Now()
    c.Next() // 실제 연산 수행
    log.Printf("%v took %v (err=%v)", c.Action(), time.Since(start), c.Err())
})
```

로깅, 실행 시간 측정, 통계 수집 같은 부수 작업에 사용합니다. 두 가지 제약이 있습니다.

**체인을 중단할 수 없습니다.** `Next()`를 호출하지 않아도 후속 핸들러와 종단 연산은 그대로 실행됩니다.
`Next()` 호출 여부는 "감싸는 위치"만 결정합니다. 연산을 거부해야 한다면 호출 측에서 사전 검사하십시오.

**백그라운드 단계의 panic은 프로세스를 종료시킵니다.** `pipequeue`의 Pipe 단계와 `timer`의 Timeout 단계는
내부 고루틴에서 실행되어 복구 지점이 없습니다. 같은 미들웨어가 모든 단계에 등록되므로,
`Action()`으로 단계를 구분하지 않으면 Put 단계에서 무해하던 코드가 Pipe 단계에서 프로세스를 내릴 수 있습니다.

## 주의사항

상세한 설명과 근거는 각 메서드의 doc comment에 있습니다. `go doc github.com/team-core-1/util/hashmap` 참고.

| 패키지 | 알아야 할 것 |
|---|---|
| `hashmap` | `Do`/`DoAll`/`All` 콜백 안에서 맵의 **어떤 메서드도** 호출하지 마십시오. 읽기 메서드도 포함이며, 재귀적 읽기 잠금이 쓰기 대기자에 막혀 데드락에 빠집니다.<br>`Put`은 신규 삽입 전용입니다. 값을 바꾸려면 `Delete` 후 `Put`을 사용하며, 이 조합은 원자적이지 않습니다. |
| `indexmempool` | `Len()`은 저장 개수가 아니라 **남은 여유 슬롯 수**입니다. 사용 중인 개수는 `Cap() - Len()`으로 구합니다. |
| `pipequeue` | `Close` 시 큐에 남은 데이터는 폐기됩니다. 내부 고루틴이 큐를 참조하므로 `New` 이후 반드시 `Close`를 호출해야 회수됩니다. |
| `timer` | `C()`를 수신하는 소비자가 반드시 있어야 합니다. 없으면 만료된 키가 정원을 계속 차지해 이후 `Set`이 `ErrExpiredQueueFull`을 반환합니다. 소비를 재개하면 정원도 회복됩니다.<br>`Close`는 대기 중인 타이머를 취소하지 않습니다. 만료된 키는 닫힌 큐로 들어가 `QFail`로만 집계됩니다. `timingWheel`의 `Start`/`Stop`은 호출 측 책임입니다. |
| `logger` | 패키지 전역 상태를 다루므로 `Init`은 프로그램 시작 시 한 번만 호출하십시오. `Close`는 파일 디스크립터만 해제하며 `fsync`하지 않습니다. |

**공통 — 핸들 객체를 `fmt`로 출력하지 마십시오.**
`Map`, `Pool`, `Queue`, `Engine`, `Timer`를 `%v`·`%+v`·`%#v`로 찍으면 `fmt`가 리플렉션으로 내부 필드를 읽습니다.
이 필드들은 뮤텍스로 보호되므로 다른 고루틴이 동시에 연산 중이면 데이터 레이스가 발생합니다.
이 패키지들은 `String()`을 제공하지 않으므로 어떤 verb도 안전하지 않습니다.

`logger`도 같은 경로를 탑니다. `logger.Info("상태", "queue", q)`처럼 핸들을 속성 값으로 넘기면
내부에서 `fmt`로 변환되어 레이스가 발생합니다.
상태를 남겨야 한다면 `Len()`, `Cap()`, `IsClosed()` 같은 접근자 값을 출력하십시오.

```go
logger.Info("상태", "len", q.Len(), "cap", q.Cap()) // 이렇게
```

## 개발 & 테스트

### 테스트 실행

1. 전체 시험

```bash
$ go test -v -race ./...
```

2. 특정 패키지 지정

```bash
$ go test -v -race ./hashmap/
```

3. 테스트 함수 지정

```bash
$ go test -v -race . -run=TestPool_BasicOps
```

4. 파일과 테스트 함수 지정

   같은 패키지 파일을 모두 나열해야 합니다. `context.go`가 빠지면 `undefined: Context`로 실패합니다.

```bash
$ go test -v -race indexmempool.go context.go indexmempool_test.go -run=TestPool_BasicOps
```

### 커버리지와 정적 검사

```bash
$ go test -cover ./...
$ go vet ./...
$ gofmt -l .
```

### 결과 읽기

테스트는 검증 항목을 `[PASS]`/`[FAIL]`로 나누어 출력하고 패키지별 요약을 냅니다.
`go test`는 통과 시 출력을 숨기므로 **결과를 보려면 `-v`가 필요합니다.**

```
── TestHashMap_Errors ──────────────────────────────────
   목적 : 정의된 에러 7종이 정확한 조건에서 반환되는지
   조건 : Capacity 3, 단일 고루틴

   [PASS] 7건
     · New(0)                    ErrInvalidCap
     · 중복 키 Put               ErrDupKey (기존 값 보존)
     ...

   결과 : 7/7 통과
```

성능 수치처럼 H/W 사양에 좌우되는 값은 `[참고]` 의 의미로 출력합니다.

출력 도우미는 [`internal/testreport`](internal/testreport/)에 있습니다.
