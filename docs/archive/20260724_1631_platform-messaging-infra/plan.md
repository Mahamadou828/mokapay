status: done

# Platform infra — River (jobs + events), MinIO/S3, Redis client, pgx

Shared prerequisites for the [payment](../20260724_1631_payment-service/plan.md)
and [file](../20260724_1631_file-service/plan.md) services, plus the redis
client the user service's rate limiting needs.

**Roadmap context (2026-07-24):** v1 ships on docker compose, ≤100 MAU,
core functionality only; v2 = e2e tests + CI + hardening; v3 = kubernetes.
Consequences for this doc: **no RabbitMQ in v1** — with exactly one event
consumer (payment), durable delivery is done with **River** jobs calling
gRPC; the broker arrives in v2+ when the notification service creates a
second independent consumer (the swap touches only the job's "deliver"
step — event payload contracts don't change). No k8s manifests in v1.

Ownership: **[M]** all River integration (Mahamadou's explicit pick);
**[C]** pgx migration, S3, cache, compose.

## 1. pgx migration **[C]**

River's first-class driver is pgx v5; the repo uses `database/sql` +
`lib/pq`. Switch the *driver*, keep the `database/sql` interfaces:

- `core/db/postgres.go`: `_ "github.com/lib/pq"` → `_ "github.com/jackc/pgx/v5/stdlib"`,
  `sql.Open("postgres", …)` → `sql.Open("pgx", …)`. Everything downstream
  (sqlc, repos, `Begin`, `ExecFolderWithTX`) is `database/sql` and stays
  untouched.
- The one code dependency on lib/pq: `user/internal/business/auth/repo`
  maps unique violations via `*pq.Error`. pgx equivalent:
  `*pgconn.PgError` (`errors.As`, code `23505`, `.ConstraintName`). Small
  helper adjustment + rerun the repo integration tests.
- Drop `lib/pq` from go.mod once nothing imports it.
- **Update 2026-07-25 — shared pgxpool**: `Postgres` now owns a
  `pgxpool.Pool` (`PgxPool`) and derives the `database/sql` handle from it
  via `stdlib.OpenDBFromPool` — one physical pool, two interfaces. River
  takes `riverpgxv5.New(pg.PgxPool)` with LISTEN/NOTIFY wakeups; repos and
  sqlc keep `pg.Pool` (`*sql.DB`) untouched. `Close()` closes the facade
  before the pool (ordering requirement of `OpenDBFromPool`).

- **Update 2026-07-25 — sqlc on pgx/v5** (user + file services, the pattern
  for payment): `sqlc.yaml` sets `sql_package: "pgx/v5"` with overrides —
  uuid→string (kept), nullable uuid→`uuid.NullUUID`, NOT NULL
  timestamptz→`time.Time` (nullable ones stay `pgtype.Timestamptz`).
  Repos: `sqlc.New(pg.PgxPool)`, `WithTx(tx pgx.Tx)`,
  `sql.Null{String,Time}`→`pgtype.{Text,Timestamptz}` (same field shapes),
  `sql.ErrNoRows`→`pgx.ErrNoRows`. Usecases: `db.BeginPgx(ctx, func(tx
  pgx.Tx) error)`. Only `migrate.go` stays on `database/sql`
  (sql-migrate + ExecFolderWithTX over `pg.Pool`).

## 2. River **[M]**

Deps: `github.com/riverqueue/river` + `riverdriver/riverpgxv5`, fed by the
shared `pg.PgxPool` (see §1 update — no second pool, no `riverdatabasesql`
poll-only fallback). The former `*sql.Tx` caveat is gone: repos/usecases
are pgx-native, so `InsertTx(ctx, tx, args, opts)` takes the same `pgx.Tx`
the confirm transaction runs on (file-service publisher does exactly
this).

What River covers (replacing every hand-rolled loop the service docs had):
- **Transactional enqueue = the outbox.** `InsertTx(tx, job)` commits the
  job atomically with the business change; workers execute after commit
  with retries/backoff; exhausted jobs land in the `discarded` state (the
  in-DB dead-letter). Consumers/handlers stay idempotent — at-least-once
  still applies (retry after a half-completed run).
- **Scheduled jobs** (`ScheduledAt`): offer expiry, client-payment timeout,
  auto-dispute, auto-confirm (payment doc §2).
- **Periodic jobs**: weekly Tuesday commission batch, file orphan sweeper.
- **Cross-service event delivery (v1 pattern)**: producer enqueues a
  `DeliverEvent` job in-tx; the worker calls the consumer's internal gRPC
  endpoint (e.g. `payment.v1.EventService.FileUploaded`) and retries until
  acked. Consumer records `event_id` in its `consumed_events` table and
  no-ops on replay.

Integration shape (per service using River — payment, file, later user):
- River's own schema migrations run inside the service's `migrate` command
  (River migration API), so `go run . migrate` stays the single migration
  entry point.
- Workers + periodic jobs are registered in `app.go`; the River client
  starts/stops with the server (same lifecycle as the gRPC server).
- Job args are small JSON structs carrying ids (+ the protojson event
  envelope for delivery jobs) — payloads stay inspectable in `river_job`.

**Event envelope & contracts** (unchanged from the broker design, so the
v2+ swap is mechanical): events are messages in a new `proto/event/v1`
package; the delivery payload is `{id uuid, type, occurred_at, producer,
payload=protojson}`. Routing-key naming (`file.document.uploaded`,
`payment.ledger_request.settled`) is kept as the `type` string.

## 3. Local infra (compose) **[C]**

- `minio` — `minio/minio`, `server /data --console-address :9001`, ports
  `9000` (S3 API) + `9001` (console), root user/pass `mokapay/mokapay1234`,
  volume `minio_data`, healthcheck on `/minio/health/live`.
- `minio-init` — one-shot `minio/mc` container creating bucket
  `mokapay-files` on first boot.
- ~~rabbitmq~~ — not in v1 (see roadmap note). k8s pairs: v3.

## 4. `core/aws/s3.go` **[C]**

Same package as the Cognito wrapper (the AWS integration package).

```go
aws.NewS3(ctx, cfg S3Config) (*S3, error)
// S3Config{Endpoint, Region, Bucket, AccessKey, SecretKey, UsePathStyle}
(*S3) PresignPut(ctx, key, contentType string, ttl time.Duration) (string, error)
(*S3) PresignGet(ctx, key string, ttl time.Duration) (string, error)
(*S3) Head(ctx, key string) (size int64, etag string, err error)
(*S3) Delete(ctx, key string) error
```

Custom `Endpoint` + `UsePathStyle=true` → MinIO locally; empty endpoint →
real S3 later. Dep: `aws-sdk-go-v2/service/s3`.

## 5. `core/cache` **[C]**

Thin `go-redis/v9` wrapper, deliberately small:

```go
cache.New(cfg Config) (*Cache, error)   // Config{Host, Port, Password, DB}
(*Cache) Get / Set(ctx, key, val, ttl)                             // cache
(*Cache) Incr(ctx, key string, ttl time.Duration) (int64, error)   // rate limits
(*Cache) SetNX(ctx, key, val, ttl) (bool, error)                   // locks
(*Cache) Close() error
```

Key-prefix convention (`cache:` evictable vs `coord:` must-not-evict);
one local redis in v1, separate policies when it matters (v3).

## 6. Verification

- `make start` → minio console on :9001, `mokapay-files` bucket exists.
- pgx swap: `make test-all` green + user repo integration tests against the
  smoke Postgres (unique-violation mapping still returns the typed errors).
- River **[M]**: a demo job round-trip in whichever service lands first —
  enqueue in a tx, worker executes, retry on forced error visible in
  `river_job`, exhausted job reaches `discarded`.
- `core/aws/s3` test behind `TEST_S3_ENDPOINT`: PresignPut → HTTP PUT →
  Head → PresignGet → Delete.
- `make check-all` / `test-all` green with no TEST_* env set (tests skip).

## Slices & Progress

- [x] Slice 1 — pgx driver swap + unique-violation helper + tests **[C]**
      — 2026-07-24. `core/db` on `pgx/v5/stdlib`, user repo maps
      `*pgconn.PgError`; migrate/seed/repo tests green on a live Postgres;
      `lib/pq` remains only as an indirect dep of sql-migrate.
- [x] Slice 2 — compose minio + bucket init **[C]** — 2026-07-24. MinIO on
      9000/9001 + one-shot `minio-init` (`mokapay-files` created, verified
      with `mc ls`).
- [x] Slice 3 — `core/cache` (unblocks user rate limiting) **[C]**
      — 2026-07-24. Get/Set, Incr (non-sliding window TTL), SetNX, Delete;
      integration test behind `TEST_REDIS_PORT` (passed against a throwaway
      redis).
- [x] Slice 4 — `core/aws/s3.go` **[C]** — 2026-07-24. PresignPut (content
      type in signature) / PresignGet / Head (`ErrObjectNotFound`) /
      idempotent Delete; round-trip test behind `TEST_S3_ENDPOINT` passed
      against the compose MinIO.
- [x] Slice 5 — River integration pattern **[M, C fixes]** — done
      2026-07-25 across the volleys: riverpgxv5 on the shared PgxPool,
      rivermigrate inside `go run . migrate` (file + payment),
      synchronous Start + deferred Stop, InsertTx for transactional
      enqueue, scheduled jobs for timers, PeriodicJobs for sweeps.
- [x] Slice 6 — **superseded** 2026-07-25: no event envelope was ever
      needed. The pattern that won is "River job → direct gRPC call
      under the system passport" (file UploadWorker → payment
      Update*Status; payment notify workers → user PushNotification),
      with idempotency provided by the receivers' status guards instead
      of a consumed-events table. `proto/event/v1` stays unused.
