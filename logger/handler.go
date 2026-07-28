package logger

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"slices"
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

type customHandler struct {
	writer     io.Writer
	level      slog.Leveler
	timeFormat string
	attrs      []slog.Attr
	group      string
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
	b := buf.AvailableBuffer()
	b = r.Time.AppendFormat(b, h.timeFormat)
	buf.Write(b)
	buf.WriteByte(' ')

	// 2. Level
	buf.WriteByte('[')
	buf.WriteString(r.Level.String())
	buf.WriteString("] ")

	// 3. Message
	buf.WriteString(r.Message)

	// 4. WithAttrs
	for _, attr := range h.attrs {
		h.appendAttr(buf, attr)
	}

	// 5. Attrs
	r.Attrs(func(attr slog.Attr) bool {
		h.appendAttr(buf, attr)
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
	h2.attrs = append(slices.Clone(h.attrs), attrs...)
	return &h2
}

func (h *customHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}

	h2 := *h
	if h2.group == "" {
		h2.group = name
	} else {
		h2.group += "." + name
	}
	return &h2
}

func (h *customHandler) appendAttr(buf *bytes.Buffer, attr slog.Attr) {
	attr.Value = attr.Value.Resolve()
	if attr.Equal(slog.Attr{}) {
		return
	}

	buf.WriteByte(' ')
	if h.group != "" {
		buf.WriteString(h.group)
		buf.WriteByte('.')
	}
	buf.WriteString(attr.Key)
	buf.WriteByte('=')
	buf.WriteString(attr.Value.String())
}
