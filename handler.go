package ezlog

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"runtime"
	"strconv"
	"sync"
	"time"
)

// ANSI escape codes used by consoleHandler.
const (
	ansiReset  = "\033[0m"
	ansiRed    = "\033[31m"
	ansiYellow = "\033[33m"
	ansiCyan   = "\033[36m"
	ansiGray   = "\033[90m"
	ansiBold   = "\033[1m"
)

// attrGroup bundles a set of slog.Attrs with the group prefix that was active
// when WithAttrs was called. This preserves correct slog group semantics:
// WithAttrs captures attrs at the current group level; a later WithGroup call
// must not retroactively nest those attrs into the new group.
type attrGroup struct {
	prefix string
	attrs  []slog.Attr
}

// consoleHandler is a slog.Handler that writes human-readable, optionally
// colored log lines.
//
// Concurrency: Handle, WithAttrs, and WithGroup may be called from multiple
// goroutines. The write to h.w is serialized with h.mu; the handler struct
// itself is treated as copy-on-write via clone().
type consoleHandler struct {
	w        io.Writer
	opts     handlerOptions
	mu       sync.Mutex
	pre      []attrGroup // attrs baked in via WithAttrs, each with snapshot prefix
	curGroup string      // active group prefix for Handle's record attrs
}

// handlerOptions is the internal config subset passed to consoleHandler.
type handlerOptions struct {
	level     slog.Leveler
	addSource bool
	noColor   bool
}

func newConsoleHandler(w io.Writer, o Options) *consoleHandler {
	lev := slog.Leveler(o.Level)
	return &consoleHandler{
		w: w,
		opts: handlerOptions{
			level:     lev,
			addSource: o.AddSource,
			noColor:   o.NoColor,
		},
	}
}

// Enabled reports whether the handler would emit a record at the given level.
func (h *consoleHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.opts.level.Level()
}

// Handle formats and writes a log record to h.w.
func (h *consoleHandler) Handle(_ context.Context, r slog.Record) error {
	buf := &bytes.Buffer{}

	// Timestamp
	if !r.Time.IsZero() {
		buf.WriteString(r.Time.Format("2006-01-02 15:04:05"))
		buf.WriteByte(' ')
	}

	// Level
	buf.WriteString(h.colorLevel(r.Level))
	buf.WriteByte(' ')

	// Message
	if !h.opts.noColor {
		buf.WriteString(ansiBold)
	}
	buf.WriteString(r.Message)
	if !h.opts.noColor {
		buf.WriteString(ansiReset)
	}

	// Pre-baked attrs (each stored with its snapshot group prefix)
	for _, ag := range h.pre {
		for _, a := range ag.attrs {
			appendAttr(buf, a, ag.prefix, h.opts.noColor)
		}
	}

	// Record-level attrs (resolved with current group prefix)
	r.Attrs(func(a slog.Attr) bool {
		appendAttr(buf, a, h.curGroup, h.opts.noColor)
		return true
	})

	// Optional source location
	if h.opts.addSource && r.PC != 0 {
		frames := runtime.CallersFrames([]uintptr{r.PC})
		f, _ := frames.Next()
		if f.File != "" {
			writeKV(buf, "source", fmt.Sprintf("%s:%d", shortenPath(f.File), f.Line), h.opts.noColor)
		}
	}

	buf.WriteByte('\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := h.w.Write(buf.Bytes())
	return err
}

// WithAttrs returns a new handler with the given attrs baked in under the
// current group prefix.
func (h *consoleHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	h2 := h.clone()
	h2.pre = append(h2.pre, attrGroup{prefix: h.curGroup, attrs: attrs})
	return h2
}

// WithGroup returns a new handler where subsequent attrs are nested under name.
// An empty name is a no-op as per the slog specification.
func (h *consoleHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	h2 := h.clone()
	if h.curGroup == "" {
		h2.curGroup = name
	} else {
		h2.curGroup = h.curGroup + "." + name
	}
	return h2
}

// clone returns a copy of h with a fresh (zero-value) mutex.
// The pre slice is duplicated so that appending to it on the copy does not
// affect the original. The inner attrGroup.attrs slices are intentionally
// shared (never mutated after creation).
func (h *consoleHandler) clone() *consoleHandler {
	h2 := &consoleHandler{
		w:        h.w,
		opts:     h.opts,
		pre:      make([]attrGroup, len(h.pre)),
		curGroup: h.curGroup,
		// mu is intentionally left zero-initialized (new, unlocked mutex).
	}
	copy(h2.pre, h.pre)
	return h2
}

// colorLevel formats the log level string, optionally with ANSI color.
func (h *consoleHandler) colorLevel(l slog.Level) string {
	if h.opts.noColor {
		switch {
		case l >= slog.LevelError:
			return "ERROR"
		case l >= slog.LevelWarn:
			return "WARN "
		case l >= slog.LevelInfo:
			return "INFO "
		default:
			return "DEBUG"
		}
	}
	switch {
	case l >= slog.LevelError:
		return ansiRed + "ERROR" + ansiReset
	case l >= slog.LevelWarn:
		return ansiYellow + "WARN " + ansiReset
	case l >= slog.LevelInfo:
		return ansiCyan + "INFO " + ansiReset
	default:
		return ansiGray + "DEBUG" + ansiReset
	}
}

// appendAttr formats a single slog.Attr into buf, recursively expanding Groups.
// prefix is the dotted group path active at this point (may be empty).
func appendAttr(buf *bytes.Buffer, a slog.Attr, prefix string, noColor bool) {
	// Resolve any slog.LogValuer implementations (including *Error).
	a.Value = a.Value.Resolve()

	// Skip zero-value attrs (e.g. ezlog.Err(nil) returns slog.Attr{}).
	if a.Equal(slog.Attr{}) {
		return
	}

	// Compute the full dotted key for this attr.
	key := joinKey(prefix, a.Key)

	if a.Value.Kind() == slog.KindGroup {
		subs := a.Value.Group()
		if len(subs) == 0 {
			return
		}
		// Inline group (empty key): sub-attrs adopt the current prefix.
		// Named group: sub-attrs nest under key.
		for _, sub := range subs {
			appendAttr(buf, sub, key, noColor)
		}
		return
	}

	writeKV(buf, key, fmtValue(a.Value), noColor)
}

// joinKey combines a prefix and a key with a dot, handling empty cases.
func joinKey(prefix, key string) string {
	switch {
	case prefix == "" && key == "":
		return ""
	case prefix == "":
		return key
	case key == "":
		return prefix
	default:
		return prefix + "." + key
	}
}

// writeKV appends " key=value" to buf, coloring the key in gray when enabled.
func writeKV(buf *bytes.Buffer, key, value string, noColor bool) {
	buf.WriteByte(' ')
	if !noColor {
		buf.WriteString(ansiGray)
	}
	buf.WriteString(key)
	buf.WriteByte('=')
	if !noColor {
		buf.WriteString(ansiReset)
	}
	buf.WriteString(value)
}

// fmtValue converts a slog.Value to a display string.
// Strings are quoted; other types use their natural representation.
func fmtValue(v slog.Value) string {
	switch v.Kind() {
	case slog.KindString:
		return strconv.Quote(v.String())
	case slog.KindBool:
		return strconv.FormatBool(v.Bool())
	case slog.KindFloat64:
		return strconv.FormatFloat(v.Float64(), 'f', -1, 64)
	case slog.KindInt64:
		return strconv.FormatInt(v.Int64(), 10)
	case slog.KindUint64:
		return strconv.FormatUint(v.Uint64(), 10)
	case slog.KindDuration:
		return v.Duration().String()
	case slog.KindTime:
		return v.Time().Format(time.RFC3339)
	default:
		return fmt.Sprintf("%v", v.Any())
	}
}

// Ensure consoleHandler satisfies slog.Handler at compile time.
var _ slog.Handler = (*consoleHandler)(nil)
