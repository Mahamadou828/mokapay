# Go review checklist

Go-specific checks. Universal rules are not repeated here. The workspace Go
style rules (`rules/go-coding-style.md`) also apply — violations of value
semantics, mapper copying, context discipline, and the single-list-query
pattern are review findings; cite the rule.

## Risk signals (scan first)

- `fmt.Println` / `log.Println` debug statements left in production code —
  services log through the structured `logger` package.
- `//nolint` comments — verify they are justified.
- `unsafe` package imports — require explicit sign-off.
- Hardcoded credentials or tokens in source.

## Code quality

- Errors returned but not checked (`_ = someFunc()`) without a justifying comment.
- `panic()` used outside of package initialization.
- Goroutines started without a clear lifetime or cancellation path.
- `interface{}` / `any` used where a concrete type, typed interface, or generic
  would work.
- Missing context propagation — `context.Context` not threaded through call
  chains that do I/O.

## Security

- `database/sql` queries built with `fmt.Sprintf` — require placeholders
  (in this workspace: sqlc named args).
- `os/exec` calls with user-controlled arguments without sanitization.
- `text/template` used for HTML output where `html/template` is required.
- TLS with `InsecureSkipVerify: true`.

## Concurrency

- Goroutines with no owner: no `context.Context`, nothing waiting on them.
- Goroutine writes to a channel with no receiver and no `select` default —
  goroutine leak.
- `time.Sleep()` used inside a goroutine as a synchronization mechanism.
- `sync.WaitGroup.Add()` called inside the goroutine it tracks — race condition.
- `sync.Mutex` copied by value — must be held by pointer or embedded in a
  struct that is never copied.

## Resource management

- `http.Response.Body` not closed after reading — even on error paths
  (`defer resp.Body.Close()`).
- `os.File` not closed — `defer f.Close()` immediately after opening.
- `rows.Close()` missing after `sql.Query()` — leaks the DB connection.
- `context.WithCancel` / `WithTimeout` cancel function not called — context and
  timer leak (see the style rule: `defer cancel()` immediately).

## Error handling

- Errors assigned to `_` without a comment explaining why it is safe.
- Errors not wrapped with `fmt.Errorf("...: %w", err)` — loses the chain.
- `errors.New` / `fmt.Errorf` strings starting with a capital letter or ending
  in punctuation.
- `panic()` for expected runtime errors.
- `recover()` swallowing panics without logging.
- Error inspection by string comparison instead of `errors.Is` / `errors.As`.

## Performance

- `append()` in a tight loop without pre-allocating capacity —
  `make([]T, 0, n)` when n is known.
- `fmt.Sprintf` for simple concatenation in hot paths — `strings.Builder` or `+`.
- `json.Marshal`/`Unmarshal` of large structs in hot paths — consider
  `json.Encoder`/`Decoder` streaming.
- A goroutine spawned per request for CPU-bound work without a worker pool.

## Idioms

- `context.Context` is always the first parameter.
- Prefer `errgroup` / `sync.WaitGroup` over ad-hoc channel coordination.
- Prefer generics over `any` for container and utility functions.
- Method receivers: value receivers per the workspace style rule; a mix of
  value and pointer receivers on the same type is a finding.
