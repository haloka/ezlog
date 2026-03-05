# ezlog

A structured logger and error library for Go — built on `log/slog`, zero external dependencies.

**Errors** carry stack traces, ordered context fields, and machine-readable codes. They integrate directly with `log/slog` so rich diagnostics flow into your logs with no boilerplate.

```
go get github.com/haloka/ezlog
```

Requires **Go 1.21+**.

---

## Why

Most Go projects use two separate things: a logging library and an error wrapping library. They don't talk to each other, so when you log an error you lose the structured context attached to it.

`ezlog` solves this in one import:

- `ezlog.Error` — a structured error with stack trace, cause chain, and key-value fields
- `ezlog.Logger` — a thin wrapper around `log/slog` that knows how to expand `*Error` values
- `ezlog.Err(err)` — the bridge: turns any error into a rich `slog.Attr`

---

## Quick Start

```go
package main

import (
    "github.com/haloka/ezlog"
)

func main() {
    log, _ := ezlog.NewLogger(ezlog.Options{})

    err := fetchUser(42)
    if err != nil {
        log.Error("request failed", ezlog.Err(err))
    }
}

func fetchUser(id int) error {
    err := queryDB(id)
    if err != nil {
        return ezlog.Wrap(err, "fetchUser").With("user_id", id)
    }
    return nil
}

func queryDB(id int) error {
    return ezlog.New("record not found").
        WithCode("DB_404").
        With("table", "users").
        With("id", id)
}
```

Output:

```
2024-01-15 10:30:45 ERROR request failed  error.msg="fetchUser: record not found" error.code="DB_404" error.user_id=42 error.table="users" error.id=42 error.stack="  main.queryDB (db/query.go:10)
  main.fetchUser (service/user.go:6)
  main.main (main.go:8)"
```

---

## Errors

### Creating errors

```go
err := ezlog.New("connection refused")
err  = ezlog.Newf("dial %s: timeout after %v", addr, timeout)
```

### Adding context

`With`, `WithCode`, and `WithFields` are **immutable** — they return a new `*Error` without modifying the receiver.

```go
err := ezlog.New("record not found").
    WithCode("DB_404").       // machine-readable code
    With("table", "users").  // ordered key-value fields
    With("id", userID)
```

```go
// Multiple fields at once (map order is not guaranteed)
err := ezlog.New("validation failed").WithFields(map[string]any{
    "field": "email",
    "value": input,
})
```

Immutability means sharing a base error is safe:

```go
base := ezlog.New("unauthorized").WithCode("AUTH_ERR")

err1 := base.With("user_id", 42)   // new instance, base is unchanged
err2 := base.With("user_id", 99)   // another new instance, independent of err1
```

### Wrapping errors

`Wrap` captures a stack trace **only if the cause chain doesn't already have one**, so the trace always points to the origin — not to every intermediate wrapper.

```go
func loadConfig(path string) error {
    data, err := os.ReadFile(path)
    if err != nil {
        // plain error → stack captured here at the origin
        return ezlog.Wrap(err, "loadConfig").With("path", path)
    }
    return nil
}

func initApp() error {
    if err := loadConfig("app.yaml"); err != nil {
        // *Error already has a stack → no duplicate capture, origin preserved
        return ezlog.Wrap(err, "initApp")
    }
    return nil
}
```

```go
return ezlog.Wrapf(err, "connect to %s:%d", host, port)
```

### Compatibility with stdlib errors

`*Error` implements `Unwrap()`, so `errors.Is` and `errors.As` traverse the full chain:

```go
var ErrNotFound = errors.New("not found")

err := ezlog.Wrap(ErrNotFound, "fetchUser").With("id", 42)

fmt.Println(errors.Is(err, ErrNotFound)) // true
```

Output:

```
true
```

### Reading error data

```go
err := ezlog.New("failed").WithCode("E001").With("key", "val")

err.Message()   // "failed"               — this layer only, no chain
err.Code()      // "E001"
err.Fields()    // []Field{{Key:"key", Value:"val"}}  — copy, safe to modify
err.Frames()    // []Frame{...}           — copy, safe to call repeatedly
err.Cause()     // wrapped error, or nil
err.Error()     // full chain: "outer: middle: base"
```

### Human-readable formatting

`FormatDetailed` dumps the full cause chain, fields, and stack trace in a readable multi-line format. It is safe to call multiple times.

```go
inner := ezlog.New("record not found").
    WithCode("DB_404").
    With("table", "users").
    With("id", 42)

outer := ezlog.Wrap(inner, "fetchUser").With("user_id", 42)

fmt.Print(ezlog.FormatDetailed(outer))
```

Output:

```
fetchUser
  - user_id: 42
caused by: record not found
  code: DB_404
  - table: users
  - id: 42
Stack:
  main.queryDB
      db/query.go:12
  main.fetchUser
      service/user.go:28
  main.main
      main.go:10
```

---

## Logger

### Creating a logger

```go
// Text output to stderr (default)
log, _ := ezlog.NewLogger(ezlog.Options{})

// Debug level, with caller file:line in every record
log, _ := ezlog.NewLogger(ezlog.Options{
    Level:     slog.LevelDebug,
    AddSource: true,
})

// JSON for production / log aggregation
log, _ := ezlog.NewLogger(ezlog.Options{
    Format: ezlog.FormatJSON,
    Level:  slog.LevelInfo,
})

// Write to any io.Writer
log, _ := ezlog.NewLogger(ezlog.Options{
    Output: myWriter,
})

// No ANSI color codes (CI, plain files)
log, _ := ezlog.NewLogger(ezlog.Options{
    NoColor: true,
})
```

### Log levels

```go
log.Debug("cache miss",  "key", "user:42")
log.Info("server ready", "addr", ":8080")
log.Warn("retry",        "attempt", 3, "max", 5)
log.Error("handler panic", ezlog.Err(err))
```

Output (text format, NoColor):

```
2024-01-15 10:30:45 DEBUG cache miss key="user:42"
2024-01-15 10:30:45 INFO  server ready addr=":8080"
2024-01-15 10:30:45 WARN  retry attempt=3 max=5
2024-01-15 10:30:45 ERROR handler panic error.msg="fetchUser: record not found" error.code="DB_404" error.user_id=42 error.table="users" error.id=42 error.stack="  ..."
```

With context:

```go
log.DebugContext(ctx, "trace", "span_id", spanID)
log.ErrorContext(ctx, "failed", ezlog.Err(err))
```

### Structured context

`With` and `WithGroup` follow `log/slog` semantics and return a new child logger:

```go
// Attach fields to every subsequent log line
reqLog := log.With("request_id", "abc-xyz", "method", "GET")
reqLog.Info("received")
reqLog.Info("processed", "status", 200, "duration_ms", 12)
```

Output:

```
2024-01-15 10:30:45 INFO  received request_id="abc-xyz" method="GET"
2024-01-15 10:30:45 INFO  processed request_id="abc-xyz" method="GET" status=200 duration_ms=12
```

```go
// Group fields under a namespace (dot-separated in text, nested object in JSON)
dbLog := log.WithGroup("db").With("host", "pg-01", "name", "users")
dbLog.Info("query", "rows", 10)
```

Output:

```
2024-01-15 10:30:45 INFO  query db.host="pg-01" db.name="users" db.rows=10
```

### AddSource

```go
log, _ := ezlog.NewLogger(ezlog.Options{AddSource: true})
log.Info("hello", "key", "val")
```

Output (source always points to the user's call site, not ezlog internals):

```
2024-01-15 10:30:45 INFO  hello key="val" source=service/user.go:42
```

### JSON format

```go
log, _ := ezlog.NewLogger(ezlog.Options{Format: ezlog.FormatJSON})
log.Info("server ready", "addr", ":8080")
log.Error("request failed", ezlog.Err(err))
```

Output:

```json
{"time":"2024-01-15T10:30:45Z","level":"INFO","msg":"server ready","addr":":8080"}
{"time":"2024-01-15T10:30:45Z","level":"ERROR","msg":"request failed","error":{"msg":"fetchUser: record not found","code":"DB_404","user_id":42,"table":"users","id":42,"stack":"  main.queryDB (db/query.go:12)\n  main.fetchUser (service/user.go:28)"}}
```

### File rotation

File rotation is not built in by design — pass any `io.Writer`. With [lumberjack](https://github.com/natefinch/lumberjack):

```go
import "gopkg.in/natefinch/lumberjack.v2"

log, _ := ezlog.NewLogger(ezlog.Options{
    Format: ezlog.FormatJSON,
    Output: &lumberjack.Logger{
        Filename:   "/var/log/myapp/app.log",
        MaxSize:    100, // MB
        MaxAge:     30,  // days
        MaxBackups: 5,
        Compress:   true,
    },
})
```

### Global default logger

```go
// Replace the package-level default (safe to call concurrently)
log, _ := ezlog.NewLogger(ezlog.Options{Format: ezlog.FormatJSON})
ezlog.SetDefault(log)

// Also sets slog's global, so slog.Info(...) routes through ezlog
slog.Info("works too", "key", "val")

// Read the current default
current := ezlog.Default()
current.Info("still works")
```

---

## `Err` — the error/log bridge

`ezlog.Err(err)` produces a `slog.Attr` that expands `*Error` values into structured log fields, including the full stack trace.

### Rich error

```go
inner := ezlog.New("db timeout").
    WithCode("DB_TIMEOUT").
    With("host", "pg-01")

outer := ezlog.Wrap(inner, "loadUser").With("user_id", 42)

log.Error("handler failed", ezlog.Err(outer))
```

Output:

```
2024-01-15 10:30:45 ERROR handler failed error.msg="loadUser: db timeout" error.code="DB_TIMEOUT" error.user_id=42 error.host="pg-01" error.stack="  main.queryDB (db/query.go:8)
  main.loadUser (service/user.go:22)"
```

Fields from **every layer** in the cause chain are collected — `user_id` from the outer wrap and `host` from the inner error both appear in the same log line.

### Plain error fallback

```go
log.Error("oops", ezlog.Err(errors.New("connection refused")))
```

Output:

```
2024-01-15 10:30:45 ERROR oops error="connection refused"
```

### Nil-safe

```go
log.Info("all good", ezlog.Err(nil))  // no "error" key produced
```

Output:

```
2024-01-15 10:30:45 INFO  all good
```

---

## slog Integration

`*Error` implements `slog.LogValuer`. When passed as an attr value directly, slog expands it into a structured group (without stack trace — use `Err()` to include the stack):

```go
log.Info("context", "err", myEzErr)
```

Output:

```
2024-01-15 10:30:45 INFO  context err.msg="record not found" err.code="DB_404" err.table="users"
```

Access the underlying `*slog.Logger` for use with slog-aware libraries:

```go
slogLogger := log.Slog()
otherLib.SetLogger(slogLogger)
```

---

## Complete Example

```go
package main

import (
    "errors"
    "fmt"
    "log/slog"

    "github.com/haloka/ezlog"
)

var ErrNotFound = errors.New("not found")

type UserService struct {
    log *ezlog.Logger
}

func NewUserService() *UserService {
    log, _ := ezlog.NewLogger(ezlog.Options{
        Level:     slog.LevelDebug,
        AddSource: true,
    })
    return &UserService{log: log.With("component", "UserService")}
}

func (s *UserService) GetUser(id int) error {
    s.log.Debug("fetching user", "id", id)

    if err := s.queryDB(id); err != nil {
        return ezlog.Wrap(err, "GetUser").With("user_id", id)
    }
    return nil
}

func (s *UserService) queryDB(id int) error {
    return ezlog.Wrap(ErrNotFound, "queryDB").
        WithCode("DB_404").
        With("table", "users").
        With("id", id)
}

func main() {
    svc := NewUserService()
    err := svc.GetUser(99)

    // Structured log
    svc.log.Error("request failed", ezlog.Err(err))

    // Human-readable debug dump
    fmt.Print(ezlog.FormatDetailed(err))

    // stdlib errors still work
    fmt.Println("Is ErrNotFound:", errors.Is(err, ErrNotFound))
}
```

Output:

```
2024-01-15 10:30:45 DEBUG fetching user component="UserService" id=99 source=service/user.go:20
2024-01-15 10:30:45 ERROR request failed component="UserService" error.msg="GetUser: queryDB: not found" error.code="DB_404" error.user_id=99 error.table="users" error.id=99 error.stack="  main.(*UserService).queryDB (service/user.go:30)
  main.(*UserService).GetUser (service/user.go:22)
  main.main (main.go:40)" source=main.go:43

GetUser
  - user_id: 99
caused by: queryDB
  code: DB_404
  - table: users
  - id: 99
caused by: not found
Stack:
  main.(*UserService).queryDB
      service/user.go:30
  main.(*UserService).GetUser
      service/user.go:22
  main.main
      main.go:40

Is ErrNotFound: true
```

---

## Design Notes

**Zero external dependencies.** Uses only the Go standard library (`log/slog`, `runtime`, `sync/atomic`). Bring your own writer for file rotation.

**Immutable error builder.** `With`, `WithCode`, and `WithFields` return new `*Error` instances. The original is never mutated — safe to share base errors across goroutines.

**Stack captured once at origin.** `Wrap` only captures a stack if the cause chain doesn't already have one. Traces always point to where the error was first created.

**`[]Frame` not `*runtime.Frames`.** Frames are stored eagerly as a plain slice. `FormatDetailed` and `Frames()` can be called any number of times without consuming state.

**Correct `AddSource` in wrapped loggers.** `Logger.Info/Debug/…` captures the caller PC directly before delegating to the handler, so `AddSource` always reports the user's call site.

**Concurrency-safe global.** `SetDefault` and `Default` use `sync/atomic.Pointer`, matching the approach used by `log/slog` itself.

**`errors.Is` / `errors.As` compatible.** `*Error` implements `Unwrap()` so the full stdlib errors API works without special cases.

---

## API Reference

### Errors

| Function / Method | Description |
|---|---|
| `New(msg) *Error` | New error with stack trace |
| `Newf(format, args...) *Error` | New error with formatted message |
| `Wrap(err, msg) *Error` | Wrap error; capture stack only if chain has none |
| `Wrapf(err, format, args...) *Error` | Wrap with formatted message |
| `(e) With(key, val) *Error` | Return new error with field appended |
| `(e) WithCode(code) *Error` | Return new error with code set |
| `(e) WithFields(map) *Error` | Return new error with multiple fields |
| `(e) Message() string` | Message at this layer only |
| `(e) Code() string` | Machine-readable code |
| `(e) Fields() []Field` | Context fields (copy) |
| `(e) Frames() []Frame` | Stack frames (copy) |
| `(e) Cause() error` | Direct cause |
| `(e) Error() string` | Full chain message |
| `FormatDetailed(err) string` | Human-readable multi-line dump |

### Logger

| Function / Method | Description |
|---|---|
| `NewLogger(opts) (*Logger, error)` | Create a logger |
| `SetDefault(l)` | Replace global default (concurrent-safe) |
| `Default() *Logger` | Get global default |
| `(l) With(args...) *Logger` | Child logger with fields |
| `(l) WithGroup(name) *Logger` | Child logger with group prefix |
| `(l) Debug/Info/Warn/Error(msg, args...)` | Log at level |
| `(l) *Context(ctx, msg, args...)` | Log at level with context |
| `(l) Slog() *slog.Logger` | Access underlying slog.Logger |
| `Err(err) slog.Attr` | Bridge: convert error to structured attr |
| `With(args...) *Logger` | Derive from global default |
| `WithGroup(name) *Logger` | Derive from global default |

---

## License

MIT
