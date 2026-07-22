# logfmt

A CLI tool that reads JSON log lines from stdin (produced by `core/logger`) and reformats them into a human-readable format.

## Usage

Pipe your application's output through logfmt:

```bash
go run ./cmd/myapp | go run ./core/logfmt
```

## Flags

| Flag | Description |
|---|---|
| `-service` | Filter output to a specific service name (case-insensitive) |

```bash
go run ./cmd/myapp | go run ./core/logfmt -service my-service
```

## Output format

```
SERVICE: TIME: FILE: LEVEL: TRACE_ID: MSG: key[value]: ...
```

Example:

```
MY-SERVICE: 2026-06-13T10:00:00Z: main.go:42: INFO: 00000000-0000-0000-0000-000000000000: server started: port[8080]:
```

Non-JSON lines (e.g. panic traces) are passed through unchanged when no `-service` filter is active.
