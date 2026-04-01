package fsutil

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"
)

// CleanLogHandler formats slog records as:
//
//	2026-04-01T09:58:51Z INFO  Message text key=value key2=value2
//
// Time is UTC ISO 8601, no quoted keys/values, no "time="/"level="/"msg=" prefixes.
type CleanLogHandler struct {
	w     io.Writer
	mu    sync.Mutex
	level slog.Leveler
	attrs []slog.Attr
	group string
}

// NewCleanLogHandler creates a handler that writes human-readable log lines.
func NewCleanLogHandler(w io.Writer, level slog.Leveler) *CleanLogHandler {
	return &CleanLogHandler{w: w, level: level}
}

// Enabled reports whether the handler handles records at the given level.
func (h *CleanLogHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level.Level()
}

// Handle formats and writes a log record.
func (h *CleanLogHandler) Handle(_ context.Context, r slog.Record) error {
	buf := make([]byte, 0, 256)

	// Time in UTC ISO 8601 (second precision)
	buf = append(buf, r.Time.UTC().Format(time.RFC3339)...)
	buf = append(buf, ' ')

	// Level (padded to 5 chars)
	switch {
	case r.Level >= slog.LevelError:
		buf = append(buf, "ERROR"...)
	case r.Level >= slog.LevelWarn:
		buf = append(buf, "WARN "...)
	case r.Level >= slog.LevelInfo:
		buf = append(buf, "INFO "...)
	default:
		buf = append(buf, "DEBUG"...)
	}
	buf = append(buf, ' ')

	// Message
	buf = append(buf, r.Message...)

	// Pre-set attrs (from WithAttrs)
	for _, a := range h.attrs {
		buf = appendAttr(buf, h.group, a)
	}

	// Record attrs
	r.Attrs(func(a slog.Attr) bool {
		buf = appendAttr(buf, h.group, a)
		return true
	})

	buf = append(buf, '\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := h.w.Write(buf)
	return err
}

// WithAttrs returns a new handler with the given attributes pre-set.
func (h *CleanLogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &CleanLogHandler{
		w:     h.w,
		level: h.level,
		attrs: append(sliceCopy(h.attrs), attrs...),
		group: h.group,
	}
}

// WithGroup returns a new handler with the given group name.
func (h *CleanLogHandler) WithGroup(name string) slog.Handler {
	g := name
	if h.group != "" {
		g = h.group + "." + name
	}
	return &CleanLogHandler{
		w:     h.w,
		level: h.level,
		attrs: sliceCopy(h.attrs),
		group: g,
	}
}

func appendAttr(buf []byte, group string, a slog.Attr) []byte {
	a.Value = a.Value.Resolve()
	if a.Equal(slog.Attr{}) {
		return buf
	}
	buf = append(buf, ' ')
	if group != "" {
		buf = append(buf, group...)
		buf = append(buf, '.')
	}
	buf = append(buf, a.Key...)
	buf = append(buf, '=')

	switch a.Value.Kind() {
	case slog.KindString:
		s := a.Value.String()
		if needsQuote(s) {
			buf = append(buf, fmt.Sprintf("%q", s)...)
		} else {
			buf = append(buf, s...)
		}
	case slog.KindDuration:
		buf = append(buf, a.Value.Duration().String()...)
	case slog.KindTime:
		buf = append(buf, a.Value.Time().UTC().Format(time.RFC3339)...)
	default:
		buf = append(buf, fmt.Sprint(a.Value.Any())...)
	}
	return buf
}

func needsQuote(s string) bool {
	for i := range len(s) {
		c := s[i]
		if c <= ' ' || c == '"' || c == '\\' {
			return true
		}
	}
	return false
}

func sliceCopy(s []slog.Attr) []slog.Attr {
	if len(s) == 0 {
		return nil
	}
	c := make([]slog.Attr, len(s))
	copy(c, s)
	return c
}

// Ensure CleanLogHandler satisfies slog.Handler at compile time.
var _ slog.Handler = (*CleanLogHandler)(nil)
