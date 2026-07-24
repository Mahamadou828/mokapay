# SQL rules

Database conventions for every service. Stack: **PostgreSQL + sql-migrate + sqlc**,
with migrations applied through `core/db` (`ExecFolderWithTX`).

## Directory structure

Every service keeps all database files under `<service>/internal/db/`:

```
internal/db/
  migrations/   # versioned schema migrations (sql-migrate)
  view/         # one file per view
  procedure/    # one file per stored procedure
  query/        # sqlc query files, one file per entity (projects.sql, tasks.sql …)
  scripts/      # init scripts run before migrations
  db.go         # embeds all four directories via //go:embed
```

Views and procedures are **not** part of migrations — they are dropped and
re-created on every `Migrate()` call via `core/db.ExecFolderWithTX()`.

---

## Migrations (`db/migrations/`)

- File naming: zero-padded numeric prefix + underscore + description in snake_case.
  `0001_create_projects.sql`, `0002_add_tasks.sql`
  The prefix ensures deterministic ordering by `sql-migrate`.
- Every migration file must include both markers:
  ```sql
  -- +migrate Up
  -- your SQL here

  -- +migrate Down
  -- rollback SQL here
  ```
- Each migration runs inside a transaction. Never use DDL that cannot be
  transacted (e.g. `CREATE INDEX CONCURRENTLY`) inside a migration file — do
  those in a separate script.

---

## Read vs. write split

- **Views** (`db/view/`) handle all **read** operations — SELECT queries exposed to sqlc.
- **Procedures** (`db/procedure/`) handle all **write** operations — add, update, soft-delete.
- Never write a raw INSERT/UPDATE/DELETE in a sqlc query file; always call the
  corresponding procedure function.

---

## Views (`db/view/`)

- One file per view, filename matches view name, snake_case: `project_summary.sql`.
- Always list columns explicitly — never use `SELECT *`. This makes the shape of
  the view self-documenting without needing to cross-reference the migration.
- Exclude `deleted_at` from the SELECT list — it is always `NULL` by definition
  (the `WHERE deleted_at IS NULL` filter) and carries no information in the view.
- All views live in the `<service>_ext` schema (e.g. `project_ext`).
  That schema is dropped and re-created on each `Migrate()` run.
- ID naming in views: when joining tables, never select both the FK column and
  the joined table's own PK — only project the FK column. This keeps column
  names unambiguous across joins.
  ```sql
  -- correct
  SELECT t.id, t.project_id, p.title AS project_title FROM tasks t JOIN projects p ON p.id = t.project_id

  -- wrong — project_id and p.id are the same value; drop p.id
  SELECT t.id, t.project_id, p.id, p.title FROM tasks t JOIN projects p ON p.id = t.project_id
  ```

---

## Schema lifecycle

`db/scripts/01_schema.sql` drops and recreates the `<service>_ext` schema. It
runs in the scripts transaction (before migrations), ensuring the schema exists
when views and procedures are applied:

```sql
DROP SCHEMA IF EXISTS project_ext CASCADE;
CREATE SCHEMA project_ext;
```

Scripts run in alphabetical order — `00_utils.sql` creates the trigger function
first, `01_schema.sql` creates the ext schema second.

---

## Procedures (`db/procedure/`)

- One file per procedure, filename matches procedure name, snake_case: `add_project.sql`.
- All procedures live in the `<service>_ext` schema.
- Every entity gets three procedures: `add_<entity>`, `update_<entity>`, `delete_<entity>`.
- Use `LANGUAGE sql` for all procedures — keeps definitions clean and avoids
  unnecessary plpgsql overhead.
- All parameters are prefixed with `p_` to avoid ambiguity with column names.

### add procedures

Required columns are explicit parameters. Optional columns default to `NULL`.
Always return the created row (`RETURNS <table>`).

```sql
CREATE OR REPLACE FUNCTION project_ext.add_project(
    p_title       TEXT,
    p_type        project_type,
    p_description TEXT           DEFAULT NULL,
    p_icon        TEXT           DEFAULT NULL,
    p_status      project_status DEFAULT 'active',
    p_due_date    DATE           DEFAULT NULL
) RETURNS projects
LANGUAGE sql AS $$
    INSERT INTO projects (title, type, description, icon, status, due_date)
    VALUES (p_title, p_type, p_description, p_icon, p_status, p_due_date)
    RETURNING *;
$$;
```

### update procedures

Every parameter except `p_id` is optional (`DEFAULT NULL`).
Use `COALESCE(p_<col>, <col>)` so passing NULL preserves the current value.
Always return the updated row.

```sql
CREATE OR REPLACE FUNCTION project_ext.update_project(
    p_id          UUID,
    p_title       TEXT           DEFAULT NULL,
    p_description TEXT           DEFAULT NULL,
    p_icon        TEXT           DEFAULT NULL,
    p_status      project_status DEFAULT NULL,
    p_type        project_type   DEFAULT NULL,
    p_due_date    DATE           DEFAULT NULL
) RETURNS projects
LANGUAGE sql AS $$
    UPDATE projects SET
        title       = COALESCE(p_title,       title),
        description = COALESCE(p_description, description),
        icon        = COALESCE(p_icon,        icon),
        status      = COALESCE(p_status,      status),
        type        = COALESCE(p_type,        type),
        due_date    = COALESCE(p_due_date,    due_date)
    WHERE id = p_id AND deleted_at IS NULL
    RETURNING *;
$$;
```

### delete procedures

Always soft-delete: set `deleted_at = now()`. Returns `VOID`.

```sql
CREATE OR REPLACE FUNCTION project_ext.delete_project(
    p_id UUID
) RETURNS VOID
LANGUAGE sql AS $$
    UPDATE projects SET deleted_at = now()
    WHERE id = p_id AND deleted_at IS NULL;
$$;
```

### calling procedures from sqlc query files

```sql
-- name: AddProject :one
SELECT * FROM project_ext.add_project(
    sqlc.arg(title),
    sqlc.arg(type)::project_type,
    sqlc.arg(description),
    sqlc.arg(icon),
    sqlc.arg(status)::project_status,
    sqlc.arg(due_date)
);
```

---

## Column conventions

- **Column order**: `id` first → business columns → `created_at`, `updated_at`, `deleted_at` last.
- **Audit columns**: every table must have:
  ```sql
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at  TIMESTAMPTZ
  ```
  `deleted_at` nullable = soft delete. Queries must filter `WHERE deleted_at IS NULL`
  unless explicitly fetching deleted rows.
- **IDs**: `UUID` everywhere. Use `gen_random_uuid()` as the default.
  ```sql
  id UUID PRIMARY KEY DEFAULT gen_random_uuid()
  ```
- **FK vs NK**:
  - `FK` = foreign key within this service → add a DB-level `REFERENCES` constraint.
  - `NK` = ID owned by another service → store as `UUID`, no DB-level FK.
  - Column naming: `<entity>_fk` for FK (e.g. `project_fk`), `<entity>_nk` for NK.

---

## Enum strategy

Use `CREATE TYPE … AS ENUM` for all enumerated columns. Enums are type-safe and
indexed efficiently by Postgres.

Constraint: adding a new value requires `ALTER TYPE … ADD VALUE` in a new
migration (cannot be done inside a transaction on Postgres < 12; on PG 12+ it
is allowed but still requires its own migration file).

Never use raw `TEXT` columns where a fixed set of values is expected — use a
named type.

---

## Tooling — sqlc

Code generation: `sqlc generate` at the service root (or the project's task
runner target that wraps it).

Config file: `sqlc.yaml` at the service root.
Generated Go code is written to `internal/sqlc/` with package name `sqlc`.

### Query files (`db/query/`)

- One file per domain entity: `projects.sql`, `tasks.sql`, `goals.sql` …
- Query names use `UpperCamelCase` with a verb prefix: `GetProject`, `ListTasks`,
  `CreateGoal`, `UpdateTaskStatus`, `DeleteReminder`.

### sqlc conventions

- **Value semantics** — never use pointer fields in generated structs.
  Set `emit_pointers_for_null_fields: false` in `sqlc.yaml`.
- **Named arguments** — always use `sqlc.arg()` / `sqlc.narg()`, never
  positional `$1 $2`.
  ```sql
  -- correct
  WHERE id = sqlc.arg(id)

  -- wrong
  WHERE id = $1
  ```
