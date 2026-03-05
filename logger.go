package ezlog

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"time"
)

// Logger wraps *slog.Logger and adds *Error-aware helpers.
//
// All methods are safe for concurrent use.
type Logger struct {
	inner *slog.Logger
}

// globalDefault holds the package-level logger. Using atomic.Pointer makes
// SetDefault and Default safe to call concurrently from multiple goroutines.
var globalDefault atomic.Pointer[Logger]

func init() {
	globalDefault.Store(must(NewLogger(DefaultOptions())))
}

func must(l *Logger, err error) *Logger {
	if err != nil {
		panic("ezlog: failed to initialize default logger: " + err.Error())
	}
	return l
}

// NewLogger creates a Logger with the given options.
// Returns an error only if the options are internally inconsistent (reserved
// for future validation); currently this always succeeds.
func NewLogger(opts Options) (*Logger, error) {
	out := opts.Output
	if out == nil {
		out = os.Stderr
	}

	var h slog.Handler
	switch opts.Format {
	case FormatJSON:
		h = slog.NewJSONHandler(out, &slog.HandlerOptions{
			Level:     opts.Level,
			AddSource: opts.AddSource,
		})
	default:
		h = newConsoleHandler(out, opts)
	}

	return &Logger{inner: slog.New(h)}, nil
}

// SetDefault replaces the package-level default logger and propagates it to
// slog's global default so that code using slog directly behaves consistently.
// Safe to call concurrently with Default and any logging calls.
func SetDefault(l *Logger) {
	globalDefault.Store(l)
	slog.SetDefault(l.inner)
}

// Default returns the current package-level default logger.
// Safe to call concurrently with SetDefault.
func Default() *Logger { return globalDefault.Load() }

// Slog returns the underlying *slog.Logger for use with slog-aware libraries.
func (l *Logger) Slog() *slog.Logger { return l.inner }

// Handler returns the underlying slog.Handler.
func (l *Logger) Handler() slog.Handler { return l.inner.Handler() }

// Enabled reports whether the logger would emit a record at the given level.
func (l *Logger) Enabled(ctx context.Context, level slog.Level) bool {
	return l.inner.Enabled(ctx, level)
}

// With returns a new Logger that includes the given key-value pairs in every
// subsequent record. Follows slog.Logger.With semantics.
func (l *Logger) With(args ...any) *Logger {
	return &Logger{inner: l.inner.With(args...)}
}

// WithGroup returns a new Logger that nests all subsequent attrs under name.
func (l *Logger) WithGroup(name string) *Logger {
	return &Logger{inner: l.inner.WithGroup(name)}
}

// Debug, Info, Warn, Error log at the respective levels.
func (l *Logger) Debug(msg string, args ...any) { l.emit(nil, slog.LevelDebug, msg, args...) }
func (l *Logger) Info(msg string, args ...any)  { l.emit(nil, slog.LevelInfo, msg, args...) }
func (l *Logger) Warn(msg string, args ...any)  { l.emit(nil, slog.LevelWarn, msg, args...) }
func (l *Logger) Error(msg string, args ...any) { l.emit(nil, slog.LevelError, msg, args...) }

// DebugContext, InfoContext, WarnContext, ErrorContext log at the respective
// levels, carrying the given context for handler use.
func (l *Logger) DebugContext(ctx context.Context, msg string, args ...any) {
	l.emit(ctx, slog.LevelDebug, msg, args...)
}
func (l *Logger) InfoContext(ctx context.Context, msg string, args ...any) {
	l.emit(ctx, slog.LevelInfo, msg, args...)
}
func (l *Logger) WarnContext(ctx context.Context, msg string, args ...any) {
	l.emit(ctx, slog.LevelWarn, msg, args...)
}
func (l *Logger) ErrorContext(ctx context.Context, msg string, args ...any) {
	l.emit(ctx, slog.LevelError, msg, args...)
}

// emit is the single log dispatch path. It captures the caller's PC so that
// AddSource always reports the user's call site, not ezlog's internal wrapper.
//
// Call stack when user calls e.g. logger.Info("msg"):
//
//	runtime.Callers  (skip 0)
//	emit             (skip 1)
//	Info/Debug/…     (skip 2)  ← public wrapper
//	user code        (skip 3)  ← we want this PC
func (l *Logger) emit(ctx context.Context, level slog.Level, msg string, args ...any) {
	if ctx == nil {
		ctx = context.Background()
	}
	if !l.inner.Enabled(ctx, level) {
		return
	}
	var pcs [1]uintptr
	runtime.Callers(3, pcs[:])
	r := slog.NewRecord(time.Now(), level, msg, pcs[0])
	r.Add(args...)
	_ = l.inner.Handler().Handle(ctx, r)
}

// --- Package-level convenience functions ---
//
// Note: package-level Debug/Info/Warn/Error functions are intentionally
// omitted because "Error" would clash with the exported Error type.
// Use ezlog.Default().Error(...) or slog.Error(...) after SetDefault instead.

// With returns a new Logger derived from the default with extra fields.
func With(args ...any) *Logger { return Default().With(args...) }

// WithGroup returns a new Logger derived from the default with a group prefix.
func WithGroup(name string) *Logger { return Default().WithGroup(name) }

// --- Error-to-slog bridge ---

// Err produces a slog.Attr with key "error" for logging an error value.
//
// For plain errors the attr value is the error string.
// For *Error values (or errors that wrap a *Error) all context fields from
// every layer in the cause chain are included, along with the stack trace
// captured at the error's origin.
//
// Usage:
//
//	logger.Error("request failed", ezlog.Err(err))
//
// When err is nil, a zero slog.Attr is returned. slog ignores zero attrs,
// so no "error" key will appear in the output.
func Err(err error) slog.Attr {
	if err == nil {
		return slog.Attr{} // slog skips zero attrs
	}

	// Fast path: plain (non-*Error) error.
	var ez *Error
	if !errors.As(err, &ez) {
		return slog.String("error", err.Error())
	}

	// Collect fields and stack from every *Error layer in the chain.
	// This ensures that context added at any wrapping layer is surfaced.
	var allFields []Field
	var frames []Frame
	var code string

	for e := err; e != nil; e = errors.Unwrap(e) {
		layer, ok := e.(*Error)
		if !ok {
			continue
		}
		allFields = append(allFields, layer.fields...)
		if len(frames) == 0 && len(layer.frames) > 0 {
			frames = layer.frames
		}
		if code == "" && layer.code != "" {
			code = layer.code
		}
	}

	attrs := make([]slog.Attr, 0, len(allFields)+4)
	attrs = append(attrs, slog.String("msg", err.Error())) // full chain message
	if code != "" {
		attrs = append(attrs, slog.String("code", code))
	}
	for _, f := range allFields {
		attrs = append(attrs, slog.Any(f.Key, f.Value))
	}
	if len(frames) > 0 {
		attrs = append(attrs, slog.String("stack", formatFramesString(frames)))
	}

	return slog.Attr{Key: "error", Value: slog.GroupValue(attrs...)}
}

// formatFramesString formats stack frames as a compact, newline-separated string.
// Each frame occupies one line: "  function (file:line)".
func formatFramesString(frames []Frame) string {
	var sb strings.Builder
	for i, fr := range frames {
		if i > 0 {
			sb.WriteByte('\n')
		}
		fmt.Fprintf(&sb, "  %s (%s:%d)", fr.Function, fr.File, fr.Line)
	}
	return sb.String()
}
