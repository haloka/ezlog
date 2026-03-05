package ezlog_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/haloka/ezlog"
)

// sentinel is a plain error type used to test interoperability.
type sentinel struct{ msg string }

func (e *sentinel) Error() string { return e.msg }

func TestNew_message(t *testing.T) {
	err := ezlog.New("something failed")
	if err.Error() != "something failed" {
		t.Fatalf("got %q", err.Error())
	}
}

func TestNew_capturesStack(t *testing.T) {
	err := ezlog.New("msg")
	if len(err.Frames()) == 0 {
		t.Fatal("New should capture stack frames")
	}
}

func TestNewf(t *testing.T) {
	err := ezlog.Newf("user %d not found", 42)
	if err.Error() != "user 42 not found" {
		t.Fatalf("got %q", err.Error())
	}
	if len(err.Frames()) == 0 {
		t.Fatal("Newf should capture stack frames")
	}
}

func TestWrap_nil(t *testing.T) {
	if ezlog.Wrap(nil, "msg") != nil {
		t.Fatal("Wrap(nil) must return nil")
	}
	if ezlog.Wrapf(nil, "msg %d", 1) != nil {
		t.Fatal("Wrapf(nil) must return nil")
	}
}

// Wrap around a plain error should capture a new stack.
func TestWrap_capturesStackForPlainError(t *testing.T) {
	plain := errors.New("native")
	wrapped := ezlog.Wrap(plain, "context")
	if len(wrapped.Frames()) == 0 {
		t.Error("Wrap should capture stack when cause has no stack")
	}
}

// Wrap around an *Error that already has a stack must NOT capture a new stack
// on the outer layer; the inner stack is preserved.
func TestWrap_preservesInnerStack(t *testing.T) {
	base := ezlog.New("base") // captures stack here
	outer := ezlog.Wrap(base, "outer")

	if len(outer.Frames()) != 0 {
		t.Error("Wrap should not capture its own stack when cause already has one")
	}
	// Inner stack must still be reachable via FormatDetailed.
	detail := ezlog.FormatDetailed(outer)
	if !strings.Contains(detail, "Stack:") {
		t.Error("FormatDetailed should print the inner stack trace")
	}
}

func TestWrapf(t *testing.T) {
	base := errors.New("base")
	err := ezlog.Wrapf(base, "user %d failed", 7)
	if err.Error() != "user 7 failed: base" {
		t.Fatalf("got %q", err.Error())
	}
}

func TestErrorChain_Is(t *testing.T) {
	root := &sentinel{msg: "record not found"}
	err := ezlog.Wrap(root, "query failed")
	err = ezlog.Wrap(err, "fetch user")

	if !errors.Is(err, root) {
		t.Error("errors.Is must find the root sentinel")
	}
}

func TestErrorChain_As(t *testing.T) {
	root := &sentinel{msg: "record not found"}
	err := ezlog.Wrap(root, "query failed")
	err = ezlog.Wrap(err, "fetch user")

	var se *sentinel
	if !errors.As(err, &se) {
		t.Fatal("errors.As must find *sentinel")
	}
	if se.msg != "record not found" {
		t.Errorf("got %q", se.msg)
	}
}

func TestErrorChain_message(t *testing.T) {
	root := &sentinel{msg: "record not found"}
	err := ezlog.Wrap(ezlog.Wrap(root, "query failed"), "fetch user")
	want := "fetch user: query failed: record not found"
	if err.Error() != want {
		t.Fatalf("got %q, want %q", err.Error(), want)
	}
}

// With must not mutate the receiver (immutable copy semantics).
func TestWith_immutable(t *testing.T) {
	base := ezlog.New("base")
	e1 := base.With("a", 1)
	e2 := base.With("b", 2)

	if len(base.Fields()) != 0 {
		t.Error("base should still have no fields")
	}
	if len(e1.Fields()) != 1 || e1.Fields()[0].Key != "a" {
		t.Error("e1 should have exactly field 'a'")
	}
	if len(e2.Fields()) != 1 || e2.Fields()[0].Key != "b" {
		t.Error("e2 should have exactly field 'b', independent of e1")
	}
}

func TestWith_chaining(t *testing.T) {
	err := ezlog.New("failed").With("k1", "v1").With("k2", 2)
	fields := err.Fields()
	if len(fields) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(fields))
	}
	if fields[0].Key != "k1" || fields[0].Value != "v1" {
		t.Errorf("field[0] wrong: %+v", fields[0])
	}
	if fields[1].Key != "k2" || fields[1].Value != 2 {
		t.Errorf("field[1] wrong: %+v", fields[1])
	}
}

func TestWithCode(t *testing.T) {
	base := ezlog.New("not found")
	coded := base.WithCode("NOT_FOUND")

	if base.Code() != "" {
		t.Error("WithCode must not mutate the receiver")
	}
	if coded.Code() != "NOT_FOUND" {
		t.Fatalf("got code %q", coded.Code())
	}
}

func TestWithFields(t *testing.T) {
	err := ezlog.New("err").WithFields(map[string]any{"x": 1, "y": 2})
	fields := err.Fields()
	if len(fields) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(fields))
	}
}

func TestNilReceiver(t *testing.T) {
	var e *ezlog.Error
	if e.Error() != "" {
		t.Error("nil.Error() should return empty string")
	}
	if e.Message() != "" {
		t.Error("nil.Message() should return empty string")
	}
	if e.Code() != "" {
		t.Error("nil.Code() should return empty string")
	}
	if e.Cause() != nil {
		t.Error("nil.Cause() should return nil")
	}
	if e.Fields() != nil {
		t.Error("nil.Fields() should return nil")
	}
	if e.Frames() != nil {
		t.Error("nil.Frames() should return nil")
	}
	if e.Unwrap() != nil {
		t.Error("nil.Unwrap() should return nil")
	}
	if e.With("k", 1) != nil {
		t.Error("nil.With() should return nil")
	}
	if e.WithCode("c") != nil {
		t.Error("nil.WithCode() should return nil")
	}
	if e.WithFields(map[string]any{"k": 1}) != nil {
		t.Error("nil.WithFields() should return nil")
	}
}

// nil *Error passed as a slog attr value must produce no output, not "<nil error>".
func TestNilLogValue_slogSkips(t *testing.T) {
	var buf bytes.Buffer
	log, err := ezlog.NewLogger(ezlog.Options{Output: &buf, NoColor: true})
	if err != nil {
		t.Fatal(err)
	}
	var e *ezlog.Error
	log.Info("result", "err", e)
	out := buf.String()
	if strings.Contains(out, "nil") {
		t.Errorf("nil *Error should produce no output, got: %q", out)
	}
	if strings.Contains(out, "err=") {
		t.Errorf("nil *Error should not produce err= attr, got: %q", out)
	}
}

func TestFormatDetailed_nil(t *testing.T) {
	if ezlog.FormatDetailed(nil) != "" {
		t.Error("FormatDetailed(nil) must return empty string")
	}
}

func TestFormatDetailed_content(t *testing.T) {
	base := ezlog.New("db error").With("table", "users").WithCode("DB_001")
	outer := ezlog.Wrap(base, "fetch failed").With("user_id", 42)

	out := ezlog.FormatDetailed(outer)

	checks := []struct {
		desc    string
		contain string
	}{
		{"outer message", "fetch failed"},
		{"inner message", "db error"},
		{"code", "DB_001"},
		{"inner field", "table: users"},
		{"outer field", "user_id: 42"},
		{"stack header", "Stack:"},
	}
	for _, c := range checks {
		if !strings.Contains(out, c.contain) {
			t.Errorf("missing %s: %q not found in:\n%s", c.desc, c.contain, out)
		}
	}
}

// FormatDetailed must be idempotent: []Frame is stored eagerly and never
// consumed, unlike *runtime.Frames which is a one-shot iterator.
func TestFormatDetailed_idempotent(t *testing.T) {
	err := ezlog.New("error")
	first := ezlog.FormatDetailed(err)
	second := ezlog.FormatDetailed(err)
	if first != second {
		t.Error("FormatDetailed should return identical output on repeated calls")
	}
	if !strings.Contains(first, "Stack:") {
		t.Error("stack should be present on every call")
	}
}

// FormatDetailed must use direct type assertion, not errors.As, to avoid
// processing the same *Error layer twice when non-*Error errors exist in chain.
func TestFormatDetailed_mixedChain(t *testing.T) {
	inner := ezlog.New("inner").With("key", "val")
	// Wrap with a plain fmt.Errorf in the middle
	middle := errors.New("middle plain")
	_ = middle // unused, but proves the chain can have non-*Error links
	outer := ezlog.Wrap(inner, "outer")

	out := ezlog.FormatDetailed(outer)

	// "inner" and "key: val" must appear exactly once.
	if count := strings.Count(out, "inner"); count != 1 {
		t.Errorf("'inner' appeared %d times, want 1:\n%s", count, out)
	}
	if count := strings.Count(out, "key: val"); count != 1 {
		t.Errorf("'key: val' appeared %d times, want 1:\n%s", count, out)
	}
}
