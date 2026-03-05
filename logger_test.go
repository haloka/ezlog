package ezlog_test

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/haloka/ezlog"
)

func newTestLogger(t *testing.T, opts ezlog.Options) (*ezlog.Logger, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	opts.Output = &buf
	opts.NoColor = true
	log, err := ezlog.NewLogger(opts)
	if err != nil {
		t.Fatalf("NewLogger: %v", err)
	}
	return log, &buf
}

func TestLogger_Info(t *testing.T) {
	log, buf := newTestLogger(t, ezlog.Options{Level: slog.LevelDebug})
	log.Info("hello world", "key", "value")
	out := buf.String()
	if !strings.Contains(out, "hello world") {
		t.Errorf("missing message in: %q", out)
	}
	if !strings.Contains(out, "key=") {
		t.Errorf("missing key attr in: %q", out)
	}
}

func TestLogger_LevelFilter(t *testing.T) {
	log, buf := newTestLogger(t, ezlog.Options{Level: slog.LevelWarn})
	log.Debug("should be hidden")
	log.Info("also hidden")
	log.Warn("visible")
	out := buf.String()
	if strings.Contains(out, "should be hidden") || strings.Contains(out, "also hidden") {
		t.Errorf("debug/info leaked through warn filter: %q", out)
	}
	if !strings.Contains(out, "visible") {
		t.Errorf("warn not printed: %q", out)
	}
}

func TestLogger_Err_richError(t *testing.T) {
	log, buf := newTestLogger(t, ezlog.Options{Level: slog.LevelDebug})
	ezErr := ezlog.New("db failed").With("table", "users").WithCode("DB_ERR")
	log.Error("request failed", ezlog.Err(ezErr))
	out := buf.String()

	for _, want := range []string{"db failed", "DB_ERR", "users", "stack="} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output: %s", want, out)
		}
	}
}

func TestLogger_Err_nil(t *testing.T) {
	log, buf := newTestLogger(t, ezlog.Options{})
	log.Info("all good", ezlog.Err(nil))
	out := buf.String()
	// A nil error must not produce any "error" key in the output.
	if strings.Contains(out, "error=") || strings.Contains(out, "error.") {
		t.Errorf("nil error produced error attr: %q", out)
	}
}

func TestLogger_Err_plainError(t *testing.T) {
	log, buf := newTestLogger(t, ezlog.Options{})
	plain := errors.New("something went wrong")
	log.Error("oops", ezlog.Err(plain))
	out := buf.String()
	if !strings.Contains(out, "something went wrong") {
		t.Errorf("plain error message missing: %q", out)
	}
	if !strings.Contains(out, "error=") {
		t.Errorf("error key missing: %q", out)
	}
}

// Err must collect fields from every layer in the cause chain.
func TestLogger_Err_collectsChainFields(t *testing.T) {
	log, buf := newTestLogger(t, ezlog.Options{})
	inner := ezlog.New("db error").With("table", "users")
	outer := ezlog.Wrap(inner, "request failed").With("request_id", "abc")
	log.Error("failed", ezlog.Err(outer))
	out := buf.String()
	if !strings.Contains(out, "users") {
		t.Errorf("inner field 'table' missing: %q", out)
	}
	if !strings.Contains(out, "abc") {
		t.Errorf("outer field 'request_id' missing: %q", out)
	}
}

// Err uses the outermost *Error's code when multiple codes exist.
func TestLogger_Err_firstCodeWins(t *testing.T) {
	log, buf := newTestLogger(t, ezlog.Options{})
	inner := ezlog.New("inner").WithCode("INNER_CODE")
	outer := ezlog.Wrap(inner, "outer").WithCode("OUTER_CODE")
	log.Error("failed", ezlog.Err(outer))
	out := buf.String()
	if !strings.Contains(out, "OUTER_CODE") {
		t.Errorf("outer code missing: %q", out)
	}
}

func TestLogger_JSON(t *testing.T) {
	log, buf := newTestLogger(t, ezlog.Options{
		Format: ezlog.FormatJSON,
		Level:  slog.LevelInfo,
	})
	log.Info("test", "key", 42)
	out := buf.String()
	if !strings.Contains(out, `"msg":"test"`) {
		t.Errorf("invalid JSON output: %q", out)
	}
	if !strings.Contains(out, `"key":42`) {
		t.Errorf("missing key in JSON: %q", out)
	}
}

func TestLogger_With(t *testing.T) {
	log, buf := newTestLogger(t, ezlog.Options{})
	child := log.With("service", "api")
	child.Info("started")
	out := buf.String()
	if !strings.Contains(out, "service=") {
		t.Errorf("inherited field missing: %q", out)
	}
}

func TestLogger_WithGroup(t *testing.T) {
	log, buf := newTestLogger(t, ezlog.Options{})
	child := log.WithGroup("req").With("id", "xyz")
	child.Info("handled")
	out := buf.String()
	if !strings.Contains(out, "req.id=") {
		t.Errorf("group prefix missing: %q", out)
	}
}

// WithGroup followed by WithAttrs: the pre-baked attrs must carry the snapshot
// prefix from when WithAttrs was called, not the handler's current group.
func TestLogger_WithGroup_attrSnapshotOrder(t *testing.T) {
	log, buf := newTestLogger(t, ezlog.Options{})

	// attrs added before WithGroup must NOT be nested
	beforeGroup := log.With("ungrouped", "yes")
	// attrs added after WithGroup MUST be nested
	afterGroup := beforeGroup.WithGroup("g").With("grouped", "yes")

	afterGroup.Info("check")
	out := buf.String()

	if !strings.Contains(out, "ungrouped=") {
		t.Errorf("pre-group attr should not be prefixed: %q", out)
	}
	if !strings.Contains(out, "g.grouped=") {
		t.Errorf("post-group attr should be prefixed with 'g.': %q", out)
	}
}

// SetDefault and Default must be race-free. Run with -race to verify.
func TestSetDefault_concurrent(t *testing.T) {
	log1, _ := ezlog.NewLogger(ezlog.Options{Output: &bytes.Buffer{}})
	log2, _ := ezlog.NewLogger(ezlog.Options{Output: &bytes.Buffer{}})

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); ezlog.SetDefault(log1) }()
		go func() { defer wg.Done(); _ = ezlog.Default() }()
	}
	// Also exercise With/WithGroup which read the global.
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); ezlog.SetDefault(log2) }()
		go func() { defer wg.Done(); _ = ezlog.With("k", 1) }()
	}
	wg.Wait()
}

func TestLogger_AddSource(t *testing.T) {
	log, buf := newTestLogger(t, ezlog.Options{AddSource: true})
	log.Info("with source") // source must point HERE, not into ezlog internals
	out := buf.String()
	if !strings.Contains(out, "source=") {
		t.Errorf("source field missing: %q", out)
	}
	// The source must reference the test file, not ezlog's internal logger.go.
	if !strings.Contains(out, "logger_test.go:") {
		t.Errorf("source should point to logger_test.go (user call site), got: %q", out)
	}
}
