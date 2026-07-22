# Go coding style

Rules that apply to **every** Go service.

---

## Layer architecture & signature design

Every service domain follows the same layer stack (see the `project` domain of
a service with SQL persistence as the reference layout):

```
internal/api/<domain>/           handlers: validate proto, call usecase
internal/api/<domain>/mapper/    proto ↔ business-type mapping (free functions)
internal/business/<domain>/      business TYPES only (read entity, NewX, UpdateX, XFilter, enums)
internal/business/<domain>/usecase/  UseCase{logger, store repo.Store}; NewUseCase(logger, db)
internal/business/<domain>/repo/     Store owns persistence; OWN models, business types in/out
```

Flow (imports only go this way): `api → business types → usecase → repo`.
The repo takes and returns **business types**; its persistence models stay
internal and convert via `toDomain() (T, error)` — a stored id that fails
`uuid.Parse` surfaces as a wrapped error, never a zero value.

### Signature rules

1. **One payload struct per operation** — never one mega-struct reused
   everywhere: `NewX` (required fields as values, optionals as pointers),
   `UpdateX` (ID + all-pointer fields, nil = "don't change"), `XFilter`
   (pointers/slices, nil/empty = "not filtering"). Optionality is expressed
   structurally, never as a long list of nullable args.
2. **Orthogonal concerns are separate typed parameters** — filtering, ordering
   and (later) paging never share a struct:
   `ListTasks(ctx, filter TaskFilter, sort TaskSort)`.
3. **No primitives where a domain type exists** — enums are typed strings whose
   constants mirror the stored values (conversion is a plain cast); ids are
   `uuid.UUID` in business signatures and plain `string` at the repo edge.
4. **Dependencies go in constructors, data goes in methods** —
   `NewUseCase(logger, db)`; a method receives only the data of its operation.
5. **Deconstructed params when a struct adds nothing** — a repo `GetX(ctx, id
   string)` takes the id directly instead of a `{ID}` wrapper.

### Type placement

- Shared domain types live at the business package root
  (`business/project/project.go`, `task.go`).
- A type used only inside the usecase is declared in the usecase package; it is
  **promoted** to the business root only when the repo needs it AND it cannot be
  deconstructed into plain parameters.
- The api layer never declares domain types — it maps proto to business types
  through its `mapper` package (free functions: `XToProto`, `CreateXToDomain`,
  `UpdateXToDomain`, `XFilterToDomain`; enum converters in `*_enum.go`).

---

## Data semantics: value by default

Use **value semantics** for all structs and whenever passing data around the
application. Structs are passed and returned by value, and method receivers are
value receivers.

```go
// correct — value receiver, value return
func NewProject(logger *logger.Logger, sqlc *sqlc.Queries) Project { ... }

func (p Project) GetProject(ctx context.Context, id uuid.UUID) (sqlc.Project, error) { ... }
```

**Pointer semantics is reserved** for the cases where copying is wrong or a
value cannot express absence:

1. **Shared, application-unique dependencies** that must not be copied — a
   `*logger.Logger`, a `*mongo.Database`/DB connection, etc. These are shared
   handles, so they are held and passed as pointers.
2. **Protobuf messages** — generated code uses pointers (`*projectv1.Project`),
   so we follow it.
3. **Optional fields** — `*T` with `nil` meaning "not provided / absent"
   (see the Optional fields section below).

Everything else (domain data, DTOs, request/response payloads) travels by value.

---

## Copying pointer-backed values in mappers

When mapping a value into a pointer field, **allocate a copy of the value** —
never take the address of a field on the input struct.

```go
// correct — copies just the formatted string, returns a *string pointing at the copy
p.DueDate = new(in.DueDate.Format(time.DateOnly))
// wrong — &field takes the address inside `in`, escaping the whole struct to the heap
p.Title = &in.Title
```

`new(expr)` here is the **Go 1.26** form: it allocates a
fresh copy of the expression's value and returns a pointer to it. This is *not*
the classic `new(T)` (which takes a type) — it takes a value, so only that value
is copied to the heap and the source struct `in` stays free to be stack-allocated.

Taking `&in.Title` instead would force escape analysis to move the entire `in`
struct onto the heap, just to keep one field's address alive.

---

## Context discipline

- `context.Context` is **always the first argument** of any function that does
  I/O, blocks, or calls something that does.
- Whenever you create a context with `context.WithTimeout` (or `WithCancel` /
  `WithDeadline`), **always call its `cancel` function in a `defer` immediately
  after**. Never discard `cancel` with `_`, and never let it be reached only on
  the success path.

```go
// correct — defer the cancel right after creating the context
ctx, cancel := context.WithTimeout(context.Background(), duration)
defer cancel()

// wrong — cancel is never called; the timer leaks until the deadline fires
ctx, _ := context.WithTimeout(context.Background(), duration)
```

---

## Error handling

- **Every returned error is checked.** Never assign an error to `_` without a
  comment explaining why it is safe to ignore.
- Wrap errors with context when passing them up:
  `fmt.Errorf("loading project %s: %w", id, err)` — always `%w`, never `%v`,
  so callers can unwrap.
- Inspect errors with `errors.Is` / `errors.As` — never string comparison.
- Error strings start lowercase and carry no trailing punctuation:
  `errors.New("project not found")`.
- `panic()` is reserved for programming errors and unrecoverable states —
  never for expected runtime failures.

---

## Concurrency

- Every goroutine must have an **owner responsible for its lifetime** — a clear
  cancellation path via `context.Context`, and something that waits for it
  (`sync.WaitGroup`, `errgroup`).
- Prefer `errgroup` over ad-hoc channel coordination.
- Never use `time.Sleep` as a synchronization mechanism.

---

## List queries: one query with optional filters, never branch in Go

A list endpoint with optional filters uses **a single sqlc query** whose filters
are optional (`narg`), passed through from a domain filter DTO. Never write one
query per filter combination and select between them with `if/else` in the
UseCase.

The query disables each filter when its argument is NULL:

```sql
-- name: ListGoals :many
-- The type filter is optional: a NULL value disables it.
SELECT id, title, type, domain, completed, created_at, updated_at
FROM project_ext.goals
WHERE (sqlc.narg(type)::goal_range IS NULL OR type = sqlc.narg(type)::goal_range)
ORDER BY created_at DESC;
```

The UseCase converts the filter DTO to the query argument and makes one call —
no branching on which filter is set:

```go
// correct — single query, filter mapped via toParams()
func (c UseCase) ListGoals(ctx context.Context, filter GoalFilter) ([]Goal, error) {
    res, err := c.sqlc.ListGoals(ctx, filter.toParams())
    ...
}

// wrong — a query per filter, selected with if/else
if filter.Type != nil {
    res, err = c.sqlc.ListGoalsByType(ctx, sqlc.GoalRange(*filter.Type))
} else {
    res, err = c.sqlc.ListGoals(ctx)
}
```

The `toParams()` helper maps the optional domain field (a `*T` pointer) to the
sqlc null type — `nil` means the filter is absent:

```go
func (f GoalFilter) toParams() sqlc.NullGoalRange {
    if f.Type == nil {
        return sqlc.NullGoalRange{}
    }
    return sqlc.NullGoalRange{GoalRange: sqlc.GoalRange(*f.Type), Valid: true}
}
```

## Optional fields: `*T` pointers

Optional fields (create/update inputs, filters, read entities, persistence
models) use `*T` pointers; `nil` means "not provided / absent". Required fields
stay plain values.

- Same-typed optionals pass the pointer straight through layer boundaries:
  `out.Title = u.Title`.
- A pointer produced from a **converted** value uses `new(expr)`:
  `out.Type = new(GoalRangeFromProto(*u.Type))`; small generic helpers like
  `ptrString[T ~string](*T) *string` cover the recurring enum-to-string case.
- Update flows write only non-nil fields — a nil field leaves the stored value
  untouched.
- Persistence models tag optional fields `,omitempty` so nil is stored as an
  absent field (and an explicit zero value stays distinguishable from unset).
