# 04 — Infrastructure & Service Boundaries

> Companion note to `04-infrastructure.drawio`. Explains the service split and
> the platform decisions behind it. Runs on **Kubernetes**, external traffic via
> an **HTTP API gateway**, events over **RabbitMQ**, per-service **Postgres** and
> **Redis**, files in **MinIO**.

---

## Guiding principle

Service boundaries follow the **transactional consistency boundary and ownership
of truth** — not code size. The expensive thing in a microservice split is
moving money across a service boundary: two services both mutating a balance
turns one ACID transaction into a distributed saga, which is exactly the
idempotency / double-count failure class the prototype got wrong (see
[`00-init.md`](../00-init.md)). So: **money that must stay consistent lives in one
service, even if that service ends up the biggest.** Fight "bloat" with internal
modules, not by splitting the database.

---

## Services

### User / Identity
Accounts (client / agent / admin), roles, authentication (OTP), agent vetting
data, contact info and notification preferences.
- **`psql user`** — its own database.
- **Redis** — rate limiting (OTP / login).

### Payments / Ledger — the money core
The single consistency boundary around money: transfers, recipient snapshots,
saved beneficiaries, `ledger_request` (+ ledger history), `agent_profile`
(`balance` / `held` / `available`), commission (accrual **and** payout), and the
money effects of disputes (capture / refund). Intentionally the largest service —
a payments service is cohesive by design.
- **`psql payments`** — its own database.
- **Redis** — FX-rate cache, FX rate-locks, idempotency keys.

### Documents
All document/proof/KYC handling: receives uploads, stores the file in MinIO plus
a row (URL + hash) in its DB, then emits events so other services react.
- **`psql docs`** — its own database.
- **MinIO** — object storage for the actual files.

### Notification
Its **own deployment** (not a sidecar). Consumes the event bus and sends SMS /
push / in-app messages. Reads contact info + preferences from User.
- **Redis** — dedupe / idempotency (optional).

### Reporting *(later)*
Read-only **CQRS projections** for history, dashboards and analytics, built by
consuming the event bus. Never a source of truth for money. Drawn dashed — not
in the v1 build.

**Dropped:** the "Management" service from the first draft. Its entities
(disputes, commission payout, transaction/ledger history) all ultimately mutate
or derive from the Payments money aggregates, so splitting them into a separate
DB would create a distributed monolith. Write-truth goes into Payments; the
read side (if it grows) becomes Reporting.

---

## Recipients

Two distinct things:
- **Recipient snapshot** — phone + name captured at transfer creation, immutable,
  part of the transfer record. Always lives on the transfer (**Payments**).
- **Saved beneficiary book** — a client's reusable contacts, used only to pre-fill
  a transfer. Lives in **Payments** for v1 (the client passes the chosen
  recipient's details into the create-transfer call, so there's no runtime
  coupling either way). Recipients are **not** users — they are never modelled as
  accounts in the User service.

---

## Data stores: per-service, never a sidecar

- **Postgres per service.** No shared database across services.
- **Redis per service** (only where needed), following the same logic as
  DB-per-service: isolation and a contained blast radius. **Not a per-pod
  sidecar** — a sidecar gives each replica its own Redis, which breaks the
  coordination use-cases that must be shared across replicas:
  - rate limits would multiply by replica count,
  - FX rate-locks in one replica would be invisible to the others,
  - idempotency keys would miss duplicates handled by a different replica.
- **Split cache from coordination.** Within a service, keep the *evicting* cache
  (`maxmemory` + LRU) separate from the *non-evicting* locks / idempotency store —
  an LRU eviction that drops a lock or an idempotency key silently reintroduces
  double-processing. Use separate instances, or a `noeviction` instance for
  coordination.
- Want locality? Use an **in-process L1 cache** (in the Go service) backed by the
  shared/per-service Redis as L2 — never a Redis sidecar.

---

## Events: RabbitMQ, not Redis pub/sub

Redis **pub/sub** is fire-and-forget: no persistence, no consumer groups, no
redelivery. If a subscriber is down when an event fires, it is gone — unacceptable
for money events like *transfer completed → accrue commission* or *dispute
resolved → capture / refund*. **RabbitMQ** is the event bus because it gives the
reliability primitives out of the box: publisher confirms, dead-letter queues,
retry/backoff, redelivery.

Redis stays — but for what it is good at: cache, FX-rate cache, rate-locks, rate
limiting, idempotency keys. It is **not** the money-event broker.

Regardless of broker, two rules:
1. **Transactional outbox.** Write the event into an `outbox` table in the *same*
   DB transaction as the state change; a relay publishes it. Never `commit DB`
   then `publish` as two steps — you can commit and crash before publishing.
2. **Idempotent consumers + DLQ.** At-least-once delivery means duplicates *will*
   happen; every consumer keys on an event id and no-ops on replay (this is the
   same rule as the `ledger_request` idempotency guard). Poison messages go to a
   dead-letter queue, not an infinite retry loop.

---

## Request path

```
Client apps (web / agent / backoffice)
        │  HTTP
        ▼
   API Gateway   — routing · authN · rate-limit
        │
        ├──▶ User / Identity
        ├──▶ Payments / Ledger
        ├──▶ Documents
        └┄┄▶ Notification   (only its read API; it is otherwise event-driven)
```

Every service publishes domain events to RabbitMQ (via its outbox); Notification
and Reporting consume. Documents also reads/writes MinIO.

---

## Open points (not yet committed)

- Whether the **notification-list read API** is served by Notification or by User
  (User owns preferences). Drawn dashed in the diagram.
- Reporting's exact read models — placeholder until the read side is needed.
- Per-service Redis vs one shared Redis is settled as **per-service**; revisit
  only if operational overhead proves too high for the team size.
