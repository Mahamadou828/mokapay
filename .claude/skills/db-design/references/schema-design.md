# Schema design reference

Distilled normalization and indexing guidance for PostgreSQL. Workspace
conventions (audit columns, soft delete, FK/NK, enums) live in `rules/sql.md`
and always apply on top of this.

## Normalization — the working subset

Target **3NF by default**; violations must be deliberate and justified.

**1NF — atomic values.** No comma-separated lists in a column, no repeating
column groups (`item1_name`, `item2_name`, …). Multi-value → child table
(`customer_phones` with a `customer_fk`). Repeating groups → rows.

**2NF — no partial dependencies.** In a junction table with a composite
business key, every non-key column must depend on the *whole* key. A
`product_name` column in `order_items` depends only on the product → it
belongs in `products`.

**3NF — no transitive dependencies.** Non-key columns must not depend on other
non-key columns. `city` + `postal_code` + `country` where city/country derive
from the postal code → extract a `postal_codes` table (or accept the
duplication consciously for address snapshots).

**When to denormalize** — only with a measured read-path reason:

- Aggregates that are expensive to compute per request (e.g. `task_count` on
  projects) — maintain via trigger or in the write procedure, and document the
  owner of the invariant.
- Snapshot columns that must *not* follow the source (e.g. price at time of
  order) — these look like 3NF violations but are correct domain modeling.
- Read-mostly reporting shapes — prefer a view first; only materialize when a
  view is provably too slow.

## Indexing patterns

**What gets an index:**

| Situation | Index |
|---|---|
| PK / UNIQUE constraint | automatic — never add manually |
| FK column used in joins/lookups | B-tree on the FK — Postgres does not auto-index FKs |
| List views ordered by recency | B-tree on `(created_at DESC)` or composite ending in it |
| Habitual filter + sort | composite: equality columns first, then range/sort columns |
| Soft-deleted tables | partial: `... WHERE deleted_at IS NULL` |
| Text search | GIN with `tsvector` (not LIKE '%…%') |
| JSONB containment queries | GIN on the jsonb column |

**Composite index rules:**

- Column order: most selective equality predicates first, range predicates last.
  An index on `(customer_fk, created_at)` serves `WHERE customer_fk = ?`,
  `WHERE customer_fk = ? AND created_at > ?`, and
  `WHERE customer_fk = ? ORDER BY created_at` — but not `WHERE created_at > ?` alone.
- A composite index makes separate single-column indexes on its leading columns
  redundant.

**Costs — why not index everything:** every index slows INSERT/UPDATE/DELETE
and consumes cache. On write-heavy tables, each index must earn its place with
a real query. Drop indexes that `pg_stat_user_indexes` shows unused.

**Operational rules:**

- `CREATE INDEX CONCURRENTLY` for indexes on large live tables — but never
  inside a migration (not transactional); use a separate script.
- Partial indexes for skewed flags (`WHERE status = 'pending'`) — an index on a
  boolean/enum column only pays off when filtering for the rare value.
- Covering indexes (`INCLUDE (col)`) when a hot query can be answered from the
  index alone — verify with `EXPLAIN (ANALYZE, BUFFERS)` that it becomes an
  Index Only Scan.

## Choosing types

- `TIMESTAMPTZ`, never `TIMESTAMP` — stores an instant, not a wall-clock guess.
- `TEXT`, not `VARCHAR(n)` — Postgres treats them identically; add a `CHECK`
  constraint if a real length limit exists.
- `NUMERIC` for money, never floats.
- `UUID` PKs per workspace rules; if insert-ordering matters for index locality,
  prefer UUIDv7 generation at the application layer.
- Named `ENUM` types for closed value sets (workspace rule); a lookup table
  instead when values need metadata or change at runtime.
