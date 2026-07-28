status: done

# File service — S3-backed uploads + upload events

Generic file storage for the platform ("Documents" in the infra doc; named
`file` here). Any kind of file, uploaded once, stored in S3 (MinIO locally),
tracked in Postgres `files`, announced via a **River delivery job** so other
services react — payment advances proof-gated transfer states, notification
(later, v2 with the broker) pings users. Fixes the prototype's "proof stored
twice" bug: files live **only** here; other services keep a `file_nk`.

Depends on: [platform-messaging-infra](../20260724_1631_platform-messaging-infra/plan.md)
(`core/aws/s3.go`, pgx swap, River pattern, compose MinIO).
Consumed by: [payment-service](../20260724_1631_payment-service/plan.md).

Owner: **[C]** end to end, except the River plumbing **[M]** (his pick;
the delivery/sweeper job handlers ride on his integration pattern).

## 1. Upload flow — presign + confirm

Bytes never transit the gateway or gRPC; the client talks to object storage
directly with short-lived credentials.

```
CreateUpload(purpose, related, filename, content_type, size)
  → validate: size cap (10 MB default, per-purpose overridable),
    content-type allowlist per purpose (images+pdf for proofs; open for OTHER)
  → insert files row (status=pending), storage_key = <purpose>/<file-uuid>
  → return {file_id, presigned PUT url (15-min TTL)}

client HTTP PUTs the bytes to the url (MinIO/S3)

ConfirmUpload(file_id)
  → Head the object: must exist, size must match the declared size
  → store etag + fetch-and-hash sha256 (streamed GET; size-capped)
  → tx: status=stored + river.InsertTx(DeliverEvent{file.document.uploaded})
    ← job commits atomically with the status flip (the outbox role)
```

- **Orphan sweeper**: River periodic job cancels `pending` rows older than
  the presign TTL (+grace) and deletes any half-uploaded object.
- Re-uploading a proof = a new file row (append, never overwrite);
  consumers use the newest `stored` file per (related, purpose).
- `RejectFile` (admin/consumer feedback, e.g. OPS rejects a deposit proof)
  flips status to `rejected` — the file stays for audit.

## 2. Reads

- `GetFile(id)` → metadata + 1-hour presigned GET (prototype convention).
- `ListFiles(related_type, related_nk)` → metadata list (newest first);
  presigned URLs only via GetFile (keeps list responses cheap and URLs
  short-lived).
- Bucket is private; presigned URLs are the only read path.

## 3. Schema (Postgres `files`, `files_ext` views/procedures, sqlc)

```
files
  id            UUID PK
  owner_nk      UUID NOT NULL          -- uploading account (user service)
  purpose       file_purpose NOT NULL  -- enum: sender_proof, agent_proof,
                                       -- recipient_proof, deposit_proof,
                                       -- kyc, other
  related_type  TEXT                   -- 'transfer' | 'ledger_request' | …
  related_nk    UUID                   -- loose NK: no FK, may precede consumer
  bucket        TEXT NOT NULL
  storage_key   TEXT NOT NULL UNIQUE   -- <purpose>/<uuid>, never client-named
  file_name     TEXT NOT NULL          -- original name, display only
  content_type  TEXT NOT NULL
  size_bytes    BIGINT NOT NULL
  sha256        TEXT                   -- set at confirm
  etag          TEXT
  status        file_status NOT NULL DEFAULT 'pending'  -- pending/stored/rejected
  + audit columns (created/updated/deleted)
```

River's tables (`river_job`, …) come from River's migrations inside
`go run . migrate`; no outbox table (transactional enqueue covers it).

View `files_ext.files` (live rows); procedures `add_file`, `confirm_file`
(status guard: only `pending → stored`), `reject_file`, `delete_file`.
Index on `(related_type, related_nk, purpose, created_at DESC)`.

No HMAC `signature` column (prototype had one): sha256 + private bucket +
presigned reads cover integrity. **Open decision F1** if parity is wanted.

## 4. Proto (`proto/file/v1`) + events

- `FileService`: CreateUpload, ConfirmUpload, GetFile, ListFiles,
  RejectFile (admin tier). Caller identity from `x-account-sub` metadata
  (same contract as the user service); `owner_nk` = resolved account id
  (see payment doc decision P1).
- Event `proto/event/v1.FileUploaded{file_id, purpose, related_type,
  related_nk, owner_nk, sha256, content_type, size_bytes}` (type string
  `file.document.uploaded`). v1 delivery: the `DeliverEvent` River job
  calls `payment.v1.EventService.FileUploaded` (via the payment client)
  and retries until acked; payment dedupes on the event id. v2+: the same
  job publishes to the broker instead — payload contract unchanged.
- Consumers: payment service (funding proof → `pending_agent_transfer`,
  execution proof → `pending_client_confirmation`, deposit proof attaches to
  the ledger_request); notification service later.

## 5. Service layout

Standard scaffold (`add-service` skill): Cobra cmd, `internal/db`
(migrations/views/procedures/queries), `internal/business/file`
(types/usecase/repo), `internal/api/file` (+mapper), `client/`, compose
entry (`CREATE DATABASE files;` init line), Makefile + vars.env entries
(k8s pair: v3). Config: DB_*, GRPC_PORT, S3_* (endpoint/bucket/keys/
path-style), PAYMENT_SERVICE_ADDR (event delivery target). The S3 client,
River client and payment gRPC client are built in `app.go` and injected
(same composition pattern as the user service's Cognito client).

## 6. Gateway routes (with the payment slice or standalone)

| Tier | Route | RPC |
|---|---|---|
| auth | `POST /v1/files` | `File.CreateUpload` |
| auth | `POST /v1/files/{id}/confirm` | `File.ConfirmUpload` |
| auth | `GET /v1/files/{id}` | `File.GetFile` |
| auth | `GET /v1/files?related_type=&related_nk=` | `File.ListFiles` |
| OPS | `POST /v1/files/{id}/reject` | `File.RejectFile` |

Authorization note: `GetFile`/`ListFiles` must check the caller owns the
file **or** is a party to the related entity (the agent must see the
client's funding proof — G9). v1 rule: owner, or OPS, or any authenticated
caller holding the related transfer — enforced by asking the payment service?
No — kept local: the file service checks `owner_nk == caller` or role ≥ OPS;
transfer-party access goes through the payment service's own endpoints,
which return file ids its handlers fetch with a service-internal GetFile.
(**Open decision F2** — see below.)

## 7. Open decisions

| # | Question | Proposal |
|---|---|---|
| F1 | HMAC signature column (prototype parity) | Skip — sha256 + private bucket + presigned reads suffice |
| F2 | Cross-party read authorization (agent reads client proof) | Party checks live in the payment service; it embeds presigned URLs (fetched via file client) in its own transfer-detail responses. File service only ever authorizes owner/OPS directly |
| F3 | Virus scanning / content inspection | Out of scope v1; the event bus makes adding a scanner consumer later trivial |
| F4 | KYC purpose | Enum value exists from day one (cheap), flows deferred (D6) |

## 8. Verification

- Unit/integration: repo tests behind `TEST_DB_PORT`; usecase test with a
  MinIO round-trip behind `TEST_S3_ENDPOINT` (create → curl PUT the
  presigned URL → confirm → row `stored` + `river_job` row enqueued →
  presigned GET returns the bytes).
- End-to-end (compose): `make start` (postgres+minio) → grpcurl
  CreateUpload/ConfirmUpload → the DeliverEvent job runs and payment's
  `consumed_events` records the event (or, before payment exists, the job
  is visible in `river_job`) → orphan sweeper cancels an unconfirmed
  upload after TTL.
- `make check-all` / `test-all` green without TEST_* env (tests skip).

## Slices & Progress

- [x] Slice 1 — scaffold + schema + protos + generated client — 2026-07-24.
      `proto/file/v1` + `proto/event/v1` (FileUploaded), full db tree
      (migration, files_ext view/procedures, sqlc), cmd/config/app/client,
      compose + Makefile + vars.env wiring (`CREATE DATABASE files`).
- [x] Slice 2 — repo — 2026-07-24. Store with WithTx (atomic
      confirm+publish seam), covered through the usecase test.
- [x] Slice 3 — usecase + S3 wiring — 2026-07-24. CreateUpload (per-purpose
      mime allowlist, size cap, server-assigned keys), ConfirmUpload (Head +
      size check + streamed sha256 + status flip and publish in ONE tx),
      GetFile/ListFiles (presigned reads), RejectFile, SweepExpired.
      Integration test (live Postgres + MinIO: full lifecycle incl.
      confirm-before-upload, double-confirm, presigned GET download) PASS;
      service boots and serves `file.v1.FileService` (verified via
      reflection). Handlers enforce the passport policy (owner/ops;
      passport-less = service-internal reads) — active once the passport
      server interceptor lands.
- [x] Slice 4 — River jobs — done 2026-07-25 across the volleys: **[M]**
      the InsertTx publisher on the confirm tx + UploadWorker skeleton;
      **[C]** the worker's three arms (deposit_proof → ledger forward,
      sender/agent proof → transfer status moves, guard rejections
      complete the job) and the 15-min orphan-sweep PeriodicJob
      (SweepWorker → uc.SweepExpired, wired before river Start).
- [x] Slice 5 — done 2026-07-25. Gateway file surface on the authed tier
      (create upload → presigned PUT → confirm → get/list) + ops reject;
      MokaClient gains the File bundle; compose wires FILE_GRPC_PORT +
      MinIO CORS pinned (preflight verified live against the compose
      MinIO). openapi + request collection carry the full presign chain.
      Authorization (F2) is the service-side owner/ops passport policy
      from slice 3, live since the interceptor landed.
