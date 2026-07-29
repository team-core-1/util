package logger

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"sync"
)

var bufPool = sync.Pool{
	New: func() any {
		return new(bytes.Buffer)
	},
}

type handlerOptions struct {
	Level      slog.Leveler
	TimeFormat string
}

// prefixedAttr은 WithAttrs로 등록된 속성을 "등록 시점의 그룹 접두사"와 함께 보관합니다.
//
// slog 규약상 WithGroup은 그 이후에 추가된 속성만 그룹으로 한정해야 합니다.
// 접두사를 핸들러에 하나만 두고 출력 시점에 일괄 적용하면,
// WithGroup 이전에 등록된 속성까지 그룹으로 묶이는 오류가 발생합니다.
//   - 예: With("a",1).WithGroup("g").With("b",2)
//     기대: a=1 g.b=2 / 오류: g.a=1 g.b=2
type prefixedAttr struct {
	prefix string
	attr   slog.Attr
}

type customHandler struct {
	writer     io.Writer
	level      slog.Leveler
	timeFormat string
	attrs      []prefixedAttr
	prefix     string // 현재 그룹 접두사 (예: "g1.g2.")
	mu         *sync.Mutex
}

func newHandler(w io.Writer, opts *handlerOptions) *customHandler {
	if opts == nil {
		opts = &handlerOptions{}
	}
	if opts.TimeFormat == "" {
		opts.TimeFormat = "2006-01-02T15:04:05.000"
	}
	if opts.Level == nil {
		opts.Level = slog.LevelInfo
	}

	return &customHandler{
		writer:     w,
		level:      opts.Level,
		timeFormat: opts.TimeFormat,
		mu:         &sync.Mutex{},
	}
}

func (h *customHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level.Level()
}

func (h *customHandler) Handle(_ context.Context, r slog.Record) error {
	buf := bufPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer bufPool.Put(buf)

	// 1. Time
	// slog 규약상 시각이 0값인 Record는 시각을 출력하지 않아야 합니다.
	// (slog.NewRecord로 Record를 직접 생성하는 경우에 해당하며,
	//  Debug/Info/Warn/Error 등 일반 경로에서는 항상 현재 시각이 채워집니다.)
	if !r.Time.IsZero() {
		b := buf.AvailableBuffer()
		b = r.Time.AppendFormat(b, h.timeFormat)
		buf.Write(b)
		buf.WriteByte(' ')
	}

	// 2. Level
	buf.WriteByte('[')
	buf.WriteString(r.Level.String())
	buf.WriteString("] ")

	// 3. Message
	buf.WriteString(r.Message)

	// 4. WithAttrs: 각 속성은 등록 시점의 그룹 접두사를 사용
	for _, pa := range h.attrs {
		h.appendAttr(buf, pa.prefix, pa.attr)
	}

	// 5. Attrs: 레코드 속성은 현재 그룹 접두사를 사용
	r.Attrs(func(attr slog.Attr) bool {
		h.appendAttr(buf, h.prefix, attr)
		return true
	})

	buf.WriteByte('\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := h.writer.Write(buf.Bytes())
	return err
}

func (h *customHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}

	h2 := *h
	// 배후 배열 공유를 피하기 위해 새 슬라이스를 할당
	h2.attrs = make([]prefixedAttr, 0, len(h.attrs)+len(attrs))
	h2.attrs = append(h2.attrs, h.attrs...)
	for _, attr := range attrs {
		// 현재(등록 시점) 접두사를 함께 고정
		h2.attrs = append(h2.attrs, prefixedAttr{prefix: h.prefix, attr: attr})
	}

	return &h2
}

func (h *customHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}

	h2 := *h
	h2.prefix = h.prefix + name + "."
	return &h2
}

func (h *customHandler) appendAttr(buf *bytes.Buffer, prefix string, attr slog.Attr) {
	attr.Value = attr.Value.Resolve()
	if attr.Equal(slog.Attr{}) {
		return
	}

	// 그룹 값 속성(slog.Group)은 그룹명을 접두사에 더해 하위 속성을 펼침
	if attr.Value.Kind() == slog.KindGroup {
		group := attr.Value.Group()
		if len(group) == 0 {
			// 빈 그룹은 출력하지 않음
			return
		}
		if attr.Key != "" {
			prefix += attr.Key + "."
		}
		for _, a := range group {
			h.appendAttr(buf, prefix, a)
		}
		return
	}

	buf.WriteByte(' ')
	buf.WriteString(prefix)
	buf.WriteString(attr.Key)
	buf.WriteByte('=')
	buf.WriteString(attr.Value.String())
}
