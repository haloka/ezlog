package ezlog

import (
	"errors"
	"fmt"
	"log/slog"
	"path"
	"runtime"
	"strings"
)

// Field is an ordered key-value pair attached to an Error.
type Field struct {
	Key   string
	Value any
}

// Frame represents a single captured stack frame.
type Frame struct {
	Function string // fully qualified function name
	File     string // source file (last two path segments)
	Line     int    // line number
}

// Error is a structured error with an optional stack trace, cause chain,
// ordered context fields, and a machine-readable code.
//
// Error values are safe to share across goroutines after construction.
// Builder methods (With, WithCode, WithFields) return new instances and
// never mutate the receiver.
type Error struct {
	msg    string
	code   string
	cause  error
	fields []Field
	frames []Frame
}

// New creates a new Error with the given message, capturing the current stack.
func New(msg string) *Error {
	return &Error{
		msg:    msg,
		frames: captureFrames(1),
	}
}

// Newf creates a new Error with a formatted message, capturing the current stack.
func Newf(format string, args ...any) *Error {
	return &Error{
		msg:    fmt.Sprintf(format, args...),
		frames: captureFrames(1),
	}
}

// Wrap wraps err with an additional context message, returning nil if err is nil.
//
// Stack is only captured if no *Error in the cause chain already carries one,
// preserving the original error origin.
func Wrap(err error, msg string) *Error {
	if err == nil {
		return nil
	}
	e := &Error{
		msg:   msg,
		cause: err,
	}
	if !chainHasFrames(err) {
		e.frames = captureFrames(1)
	}
	return e
}

// Wrapf wraps err with a formatted context message, returning nil if err is nil.
func Wrapf(err error, format string, args ...any) *Error {
	if err == nil {
		return nil
	}
	e := &Error{
		msg:   fmt.Sprintf(format, args...),
		cause: err,
	}
	if !chainHasFrames(err) {
		e.frames = captureFrames(1)
	}
	return e
}

// With returns a new Error with the given key-value field appended.
// The receiver is not modified.
func (e *Error) With(key string, value any) *Error {
	if e == nil {
		return nil
	}
	return e.copyWith(Field{Key: key, Value: value})
}

// WithCode returns a new Error with the given machine-readable code set.
// The receiver is not modified.
func (e *Error) WithCode(code string) *Error {
	if e == nil {
		return nil
	}
	c := e.shallowCopy()
	c.code = code
	return c
}

// WithFields returns a new Error with all provided fields appended.
// Map iteration order is not guaranteed; for stable field order prefer
// chaining With calls instead.
// The receiver is not modified.
func (e *Error) WithFields(fields map[string]any) *Error {
	if e == nil {
		return nil
	}
	c := e.shallowCopy()
	c.fields = make([]Field, len(e.fields), len(e.fields)+len(fields))
	copy(c.fields, e.fields)
	for k, v := range fields {
		c.fields = append(c.fields, Field{Key: k, Value: v})
	}
	return c
}

// Message returns the error message at this layer, without the cause chain.
func (e *Error) Message() string {
	if e == nil {
		return ""
	}
	return e.msg
}

// Code returns the machine-readable error code, or empty string if none is set.
func (e *Error) Code() string {
	if e == nil {
		return ""
	}
	return e.code
}

// Cause returns the direct cause of this error, or nil.
func (e *Error) Cause() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// Fields returns a copy of the context fields attached to this layer.
func (e *Error) Fields() []Field {
	if e == nil || len(e.fields) == 0 {
		return nil
	}
	out := make([]Field, len(e.fields))
	copy(out, e.fields)
	return out
}

// Frames returns a copy of the captured stack frames.
// Unlike *runtime.Frames, this can be called multiple times safely.
func (e *Error) Frames() []Frame {
	if e == nil || len(e.frames) == 0 {
		return nil
	}
	out := make([]Frame, len(e.frames))
	copy(out, e.frames)
	return out
}

// Error implements the error interface, returning the full message chain.
func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.cause != nil {
		return e.msg + ": " + e.cause.Error()
	}
	return e.msg
}

// Unwrap enables errors.Is and errors.As to traverse the cause chain.
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// LogValue implements slog.LogValuer, returning a structured Group with
// message, code, fields, and cause string.
//
// Stack frames are intentionally omitted here; use ezlog.Err(e) to produce
// a slog.Attr that includes the full stack trace.
func (e *Error) LogValue() slog.Value {
	if e == nil {
		// Return an empty GroupValue. slog skips empty groups, so a nil *Error
		// passed as an attr value produces no output — consistent with the
		// nil-safe contract of all other methods on this type.
		return slog.GroupValue()
	}
	attrs := make([]slog.Attr, 0, len(e.fields)+3)
	attrs = append(attrs, slog.String("msg", e.msg))
	if e.code != "" {
		attrs = append(attrs, slog.String("code", e.code))
	}
	for _, f := range e.fields {
		attrs = append(attrs, slog.Any(f.Key, f.Value))
	}
	if e.cause != nil {
		attrs = append(attrs, slog.String("cause", e.cause.Error()))
	}
	return slog.GroupValue(attrs...)
}

// shallowCopy returns a shallow copy of the Error with the fields slice
// duplicated so that future appends on the copy do not affect the original.
func (e *Error) shallowCopy() *Error {
	c := *e
	c.fields = make([]Field, len(e.fields))
	copy(c.fields, e.fields)
	// frames is never mutated after capture; sharing is safe.
	return &c
}

// copyWith returns a new Error identical to e but with one extra field appended.
func (e *Error) copyWith(f Field) *Error {
	c := *e
	c.fields = make([]Field, len(e.fields)+1)
	copy(c.fields, e.fields)
	c.fields[len(e.fields)] = f
	return &c
}

// chainHasFrames reports whether any *Error in the cause chain already carries
// captured stack frames. Non-*Error errors in the chain are skipped.
//
// Only single-error Unwrap chains are traversed; errors.Join trees are not
// fully explored (limitation of v1).
func chainHasFrames(err error) bool {
	for err != nil {
		if ez, ok := err.(*Error); ok && len(ez.frames) > 0 {
			return true
		}
		err = errors.Unwrap(err)
	}
	return false
}

// captureFrames records the call stack starting at the caller's caller.
//
// skip=1: skip captureFrames itself and one additional frame (New, Wrap, etc.)
// so that the first recorded frame is the user's call site.
//
// Frames belonging to the "runtime" and "testing" packages are excluded.
func captureFrames(skip int) []Frame {
	pc := make([]uintptr, 128)
	// runtime.Callers skip semantics:
	//   0 = runtime.Callers itself
	//   1 = captureFrames
	//   2+skip = caller of captureFrames (New/Wrap/...) and beyond
	n := runtime.Callers(skip+2, pc)
	if n == 0 {
		return nil
	}
	rframes := runtime.CallersFrames(pc[:n])
	out := make([]Frame, 0, n)
	for {
		f, more := rframes.Next()
		if !strings.HasPrefix(f.Function, "runtime.") &&
			!strings.HasPrefix(f.Function, "testing.") {
			out = append(out, Frame{
				Function: f.Function,
				File:     shortenPath(f.File),
				Line:     f.Line,
			})
		}
		if !more {
			break
		}
	}
	return out
}

// shortenPath returns the last two slash-separated segments of a file path.
// Go toolchain source paths always use forward slashes, even on Windows.
//
// Example: "/home/user/proj/internal/db/repo.go" → "db/repo.go"
func shortenPath(fpath string) string {
	dir, file := path.Split(fpath)
	if dir == "" {
		return file
	}
	dir = strings.TrimSuffix(dir, "/")
	parent := path.Base(dir)
	if parent == "." || parent == "/" || parent == "" {
		return file
	}
	return parent + "/" + file
}
