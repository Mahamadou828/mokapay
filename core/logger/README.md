# logger

A structured logging package built on top of Go's `log/slog`, emitting JSON to any `io.Writer`. Designed to be piped through `core/logfmt` for human-readable output during development.

## Creating a logger

```go
import "mokapay.app/core/logger"

log := logger.New(os.Stdout, logger.LevelInfo, "my-service", nil)
```

**Parameters:**
- `w io.Writer` — destination for log output (e.g. `os.Stdout`)
- `minLevel logger.Level` — minimum level to emit (`LevelDebug`, `LevelInfo`, `LevelWarn`, `LevelError`)
- `serviceName string` — added to every log line as the `service` field
- `traceIDFn logger.TraceIDFn` — optional function to extract a trace ID from a `context.Context`; pass `nil` if not needed

## Logging

All methods accept a `context.Context` and a message, followed by optional key-value pairs:

```go
ctx := context.Background()

log.Info(ctx, "server started", "port", 8080)
log.Warn(ctx, "high memory usage", "used_mb", 900)
log.Error(ctx, "request failed", "status", 500, "path", "/api/users")
log.Debug(ctx, "cache hit", "key", "user:42")
```

Each call produces a JSON line like:

```json
{"time":"2026-06-13T10:00:00Z","level":"INFO","msg":"server started","service":"my-service","file":"main.go:25","port":8080}
```

## Trace ID injection

Supply a `TraceIDFn` to automatically attach a `trace_id` to every log entry from context:

```go
func traceIDFromCtx(ctx context.Context) string {
    if id, ok := ctx.Value(traceKey{}).(string); ok {
        return id
    }
    return ""
}

log := logger.New(os.Stdout, logger.LevelInfo, "my-service", traceIDFromCtx)
```

## Event hooks

Use `NewWithEvents` to run a callback when a log entry at a given level is emitted — useful for metrics or alerting:

```go
events := logger.Events{
    Error: func(ctx context.Context, r logger.Record) {
        // e.g. increment an error counter
        errorCounter.Inc()
    },
}

log := logger.NewWithEvents(os.Stdout, logger.LevelInfo, "my-service", nil, events)
```

## Build info

Log Go build settings (flags, VCS info, Go version, module version) at startup:

```go
log.BuildInfo(ctx)
```

## Using with logfmt

`core/logfmt` is a companion CLI that reads the JSON log lines from stdin and reformats them for readability. Run it by piping your application's output through it:

```bash
go run ./cmd/myapp | go run ./core/logfmt
```

**Filter by service name** with the `-service` flag:

```bash
go run ./cmd/myapp | go run ./core/logfmt -service my-service
```

Non-JSON lines (e.g. panics) are passed through as-is when no service filter is active.

**Output format:**

```
SERVICE: TIME: FILE: LEVEL: TRACE_ID: MSG: key[value]: ...
```

Example:

```
MY-SERVICE: 2026-06-13T10:00:00Z: main.go:25: INFO: 00000000-0000-0000-0000-000000000000: server started: port[8080]:
```
