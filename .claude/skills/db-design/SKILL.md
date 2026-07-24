---
name: db-design
description: Design or extend a PostgreSQL schema for a service — new entities, tables, views, procedures, sqlc queries, and migrations following the workspace SQL conventions. Use when adding an entity to a service, designing a schema, planning a migration, or deciding on indexes.
---

# Database design

Design schema changes for the workspace stack: **PostgreSQL + sql-migrate +
sqlc**, with the read/write split (views for reads, procedures for writes)
defined in the workspace SQL rules.

## 0. Load the rules

Read `${CLAUDE_SKILL_DIR}/../../rules/sql.md` first (or the consuming project's
`.claude/rules/sql.md` if it has one — project rules win). Every artifact this
skill produces must follow it: directory layout, naming, audit columns, soft
delete, enum strategy, `p_` parameters, `sqlc.arg()`.

## 1. Model the entity

Before writing SQL, settle these with the user or from the feature context:

- **Fields**: which are required, which optional, which enumerated (→ named
  enum type, never TEXT).
- **Relationships**: for each related entity, is it owned by *this* service
  (→ `<entity>_fk` with a `REFERENCES` constraint) or by *another* service
  (→ `<entity>_nk`, plain UUID, no constraint)?
- **Cardinality**: 1:N → FK on the N side. N:M → junction table named
  `<a>_<b>` with both FKs and its own audit columns.
- **Normalization**: default to 3NF — no repeating groups, no multi-value
  columns, non-key columns depend on the whole key and nothing but the key.
  Denormalize only for a measured read-path reason, and say so.
  Details: [references/schema-design.md](references/schema-design.md).

## 2. Produce the artifacts, in order

For a new entity `goal` in service `example`:

1. **Migration** — `internal/db/migrations/<next-number>_create_goals.sql` with
   `-- +migrate Up` / `-- +migrate Down`, enum types first, then the table
   (id UUID default `gen_random_uuid()`, business columns, audit columns last).
2. **View** — `internal/db/view/goals.sql` in `<service>_ext`, explicit column list,
   `WHERE deleted_at IS NULL`, no `deleted_at` in the SELECT.
3. **Procedures** — `internal/db/procedure/add_goal.sql`, `update_goal.sql`,
   `delete_goal.sql` per the rules (COALESCE updates, soft delete).
4. **sqlc queries** — `internal/db/query/goals.sql`: reads against the view, writes
   through the procedures, optional list filters via `sqlc.narg()` in a single
   query.
5. **Generate** — run `sqlc generate` (or the project's wrapper target) and fix
   any type mismatches.

## 3. Indexes

Postgres creates indexes for primary keys and unique constraints automatically —
don't duplicate them. Add an index only for a query the service actually runs:

- FK columns used in joins (`project_fk`) — Postgres does **not** auto-index FKs.
- Columns in a view's habitual WHERE/ORDER BY (e.g. `created_at DESC` for lists).
- Composite indexes: most selective / equality columns first, range columns last;
  the index also serves any leading prefix of its columns.
- Partial indexes for soft delete: since every read filters `deleted_at IS NULL`,
  prefer `CREATE INDEX ... WHERE deleted_at IS NULL` — smaller and faster.
- Never `CREATE INDEX CONCURRENTLY` inside a migration (not transactional) —
  separate script.

More patterns: [references/schema-design.md](references/schema-design.md).

## 4. Sanity checks before finishing

- Down migration actually reverses the Up (drops table *and* its enum types).
- Every enum column has a named type.
- Procedures return the row (`RETURNS <table>`) except delete (`RETURNS VOID`).
- View column list matches what the sqlc queries select.
- `sqlc generate` runs clean.
