status: approved

# Payment service — transfers, matching, ledger, commissions

The money core ("intentionally the largest service" — infra doc). Owns
transfers + recipient snapshots, saved beneficiaries, the matching queue,
`agent_profile` (balance / held / available), holds, `ledger_request` + the
immutable ledger, commission accrual & weekly payout, and the money effects
of disputes. Postgres `payments`, Redis (FX cache, rate locks), **River**
(background jobs, timers, transactional event delivery — no broker in v1,
see the platform-infra doc's roadmap note).

Sources: `docs/01-init/01-project-spec.md` (state machines quoted below),
`docs/01-init/00-init.md` (prototype teardown — the bugs this design fixes),
`docs/01-init/01-project-specs/04-infrastructure.md`.
Depends on: [platform-messaging-infra](../20260724_1631_platform-messaging-infra/plan.md)
(pgx swap, River pattern, cache), [file-service](../20260724_1631_file-service/plan.md)
(proof events).

## Ownership split (agreed 2026-07-24)

- **[M] Mahamadou — the critical core**: the **matching algorithm**
  (eligibility, 7-criteria scoring, offer lifecycle, timeout + re-match
  loop), the **ledger_request engine** (settlement idempotency, hold
  release/capture, the only code paths that mutate `balance`/`held`), and
  the **River integration** (client lifecycle, workers, scheduled/periodic
  jobs — his explicit pick).
- **[C] Claude — everything around it**: scaffold + infra wiring, full DB
  layer (migrations/views/procedures/sqlc — including the procedures the
  matching/ledger usecases call: M implements logic, not plumbing), protos,
  repo layer + integration tests, fx/quote, transfer create/read/timeline
  with the guarded transition table, beneficiaries, deposit/withdrawal
  request flows up to the ledger-engine handoff, dispute records, commission
  accrual + read models, the `EventService.FileUploaded` receiving RPC +
  `consumed_events` dedupe, api handlers + mappers (M's usecases get thin
  handlers once they land).

## 1. Money representation

All amounts are **BIGINT minor units** + a currency enum (`eur`, `cdf`,
`usd`) — never floats, never a bare `*_usd` column holding non-USD values
(teardown #9). Rates are stored as NUMERIC(18,6). v1 corridor: client pays
EUR, recipient receives CDF (~2850 CDF/EUR); the agent guarantee is USD.

## 2. Transfer domain

> Detailed design (corridors, FX provider, broadcast matching, accept
> race, proto surface): [transfer.md](transfer.md) — supersedes this
> section and §4 where they differ (notably: **no EUR** — corridors are
> USD→CDF / USD→USD / CDF→CDF; broadcast offers instead of the
> sequential walk; no holds table; no Redis quote-lock).

### Status machine (spec, verbatim states)

```
pending_match → pending_agent_approval → pending_client_transfer
  → pending_agent_transfer → pending_client_confirmation → done
bad paths: disputed → { refunded | done | cancelled },  expired, cancelled
```

Rules fixed at creation: rate locked once (15-min quote TTL, D11
auto-re-quote), fee (~2%) + FX margin (0.5%) computed once, commission
computed once (flat 60% of fee, D9). Guarantee hold: reserved on agent
accept, released on done, captured only by dispute resolution.

Transitions live in **one guarded table** (`map[status][]status` +
procedure-level status guards) — never an unguarded status setter (teardown:
prototype's `UpdateStatus` accepted arbitrary strings). Every transition
writes a `transfer_events` row (actor, from, to, reason) — the C13 timeline.

Proof-gated transitions come from the file service's events, not uploads
stored here (teardown #5 — proofs live only in the file service; transfers
reference `file_nk`). Delivery: the file service's River job calls this
service's internal `EventService.FileUploaded` RPC (idempotent via
`consumed_events(event_id)`):
- `file.document.uploaded{purpose=sender_proof}` → `pending_client_transfer
  → pending_agent_transfer`
- `{purpose=agent_proof}` → `pending_agent_transfer →
  pending_client_confirmation`

Timers = **River scheduled jobs** (enqueued in the same tx as the
transition that starts the clock; job no-ops if the state moved on):
- `pending_agent_approval`: offer expiry → re-match **[M]**
- `pending_client_transfer`: client never pays → timeout → `cancelled`,
  release hold
- `pending_agent_transfer`: agent never pays → **auto-dispute** (spec)
- `pending_client_confirmation`: no reaction in Y → **auto-confirm** → done
  (D11)

### Matching **[M]**

Offer/accept with timeout + re-match (D8). Eligibility: agent `active`,
`balance ≥ guarantee floor`, `available = balance − held ≥ transfer
guarantee`, wallet covers the recipient operator. Scoring 0–100:
availability 25, guarantee 20, wallet fit 20, success rate 15, level 10,
geo 5, load 5 (prototype §5.3; city hardcoding is *not* carried over — geo
input comes from agent profile city vs recipient city, nullable in v1).
Self-deal guard (agent ≠ transfer's client). Match failure leaves
`pending_match` + schedules retry. Accept runs atomically: offer accepted →
hold placed → transfer to `pending_client_transfer` (single transaction —
procedure provided by [C], invoked by [M]'s usecase).

## 3. Ledger domain

> Detailed design (schemas, CTE settlement, RPC surface, enrollment
> linkage): [ledger.md](ledger.md) — supersedes the sketch below where
> they differ.

### agent_profile

`account_nk UNIQUE` (the User service account), status
(`pending/active/suspended`), level (`n1..n4`, N4 requires insurance flag),
guarantee floor (minor-units USD, default 20 USD), `balance`, `held`,
wallets in `agent_wallets` (operator enum, msisdn, is_default). `available`
is **derived in the view only** (`balance − held`) — never stored (teardown
#6). Stats (success rate, completed count) are view-derived from transfers.

### ledger_request **[M engine, C plumbing]**

The only way `balance` moves. Mutable workflow object:

```
type:   funding(+) | withdrawal(−) | refund(±) | deduction(−) | commission(+)
status: initiated → pending_agent_action → pending_admin_approval
        → { done | rejected | disputed }
```

Settlement (→ `done`) writes exactly one **immutable ledger line**
(`ledger_lines`: ledger_request_fk **UNIQUE** — self-idempotent, teardown
flow-#1; signed amount; `balance_after`) and updates `balance`, all inside
one transaction with `SELECT … FOR UPDATE` on the agent profile + a status
guard (`WHERE status <> 'done'`). `idempotency_key UNIQUE` on the request
covers producer-side retries (event redeliveries).

### Holds **[M engine, C plumbing]**

Auth/capture pattern; a hold is *not* a ledger_request. `holds` rows move
`held` only, through procedures:
- `place_hold(agent, ref, amount)` → `held += X` (guard: `available ≥ X`)
- `release_hold(id)` → `held −= X` (happy path; no ledger_request)
- `capture_hold(id)` → `held −= X` **and** creates+settles a `deduction`
  ledger_request — the single hold→balance crossover (spec invariant).

### Commission

On `transfer → done`: accrue one `commissions` row (transfer_fk UNIQUE —
single bookkeeping place, teardown #4): agent 60% of fee, platform 30%,
guarantee fund 10%, FX margin 100% platform, `accounting_week` (Tue→Mon).
Weekly Tuesday batch (`commission_batches`): groups unpaid accruals per
agent and settles each through a `commission` ledger_request. Batch run is a
**River periodic job** (Tuesday cron); a `settle-commissions` CLI command
stays as the manual/ops re-run entry point.

## 4. Other domains

- **fx** **[C]**: `Quote(amount_eur) → {rate, fee, margin, amount_cdf,
  quote_id, expires_at}`. v1 rate = configured value (env) with the plumbing
  shaped for a provider later; quote stored in Redis (`coord:` prefix,
  15-min TTL); `CreateTransfer(quote_id)` consumes the lock (SETNX-style
  single use).
- **beneficiary** **[C]**: saved recipients per client (D7): name, phone
  (E.164, reuse `nyaruka/phonenumbers` normalization), operator. CRUD.
- **dispute** **[C records, M money effects]**: `disputes` row per transfer;
  admin resolution enum drives the money: `agent_right → release, done`;
  `agent_fault → capture + refund ledger_request, refunded`; `client_fraud →
  release, done`; `no_fault → release, cancelled`. Hold **stays held** while
  disputed (spec core rule).

## 5. Schema (tables → files per SQL rules: `payments_ext` views + procedures, sqlc)

`agent_profiles`, `agent_wallets`, `transfers` (+ `transfer_ref` sequence →
`MKP-YYYY-NNNNNN`), `transfer_events`, `offers` (transfer_fk,
agent_profile_fk, score, status `offered/accepted/declined/expired`,
expires_at), `holds`, `ledger_requests`, `ledger_lines`, `commissions`,
`commission_batches`, `disputes`, `beneficiaries`, consumed-events
idempotency table (`consumed_events(event_id UUID PRIMARY KEY,
consumed_at)`). River's own tables (`river_job`, …) are created by River's
migrations inside `go run . migrate`. No `outbox` table — transactional
enqueue via River `InsertTx` plays that role.

Conventions: enums as native types mirroring the business strings; audit
columns everywhere; soft delete only where it makes sense (beneficiaries,
wallets — **not** on ledger_lines/transfer_events, which are append-only and
immutable); `*_nk` for cross-service ids (account_nk, file_nk), `*_fk` with
REFERENCES inside the service.

## 6. Protos (`proto/payment/v1`) + events

- `TransferService`: Quote, CreateTransfer, GetTransfer, ListTransfers
  (filters: status), GetTransferTimeline, ConfirmReceipt, OpenDispute.
- `AgentService` (caller from `x-account-sub` metadata, same identity
  contract as the user service): GetMyProfile, ListOffers, AcceptOffer,
  DeclineOffer, MarkTransferPaid, ListMovements, InitiateDeposit,
  RequestWithdrawal, ListCommissions, wallet CRUD.
- `AdminPaymentService`: ListLedgerRequests, ValidateDeposit,
  RejectDeposit, ApproveWithdrawal, ResolveDispute, GetAgentProfile,
  ListAgentProfiles, CreateAgentProfile (enrollment hook).
- `EventService` (internal tier — never exposed on the gateway): the
  receiving end of River-delivered events, idempotent via `consumed_events`.
  v1: `FileUploaded(event.v1.FileUploaded)`.
- Events produced (`proto/event/v1` messages, delivered by River jobs
  enqueued in-tx): `transfer.created/accepted/funded/executed/completed/
  disputed/resolved/expired` (no consumer in v1 — enqueue only where a
  consumer exists; the enum of types is reserved), `ledger_request.settled`
  → delivered to the user service (enrollment activation),
  `commission.batch_paid` (v2, notification).

## 7. Cross-service handshakes

- **Enrollment** (user-service doc §4): `AdminPaymentService.
  CreateAgentProfile(account_nk, enrollment_nk)` called at invite-accept
  time creates the profile + the 20-USD `funding` ledger_request; its
  settlement event promotes the account in the user service.
- **Identity**: gateway forwards `x-account-sub`; this service resolves
  agent profiles by `account_nk` — it needs the account id, not the sub, so
  the gateway (or this service via user client) resolves sub → account id.
  **Decision P1 (open)**: forward `x-account-id` too (gateway resolves once,
  cached) — proposed — vs each service calling the user service.

## 8. Open decisions

| # | Question | Proposal |
|---|---|---|
| P1 | sub → account-id resolution | Gateway resolves via user client + cache, forwards `x-account-id` alongside `x-account-sub` |
| P2 | Client payment details shown at C11 (how the client pays the agent off-platform) | agent_wallets marked `is_default` per operator; shown on the matched transfer |
| P3 | Timer cadence / infrastructure | River scheduled jobs (settled 2026-07-24 — replaces the ticker-loop idea) |
| P4 | Refund shortfall (D10) | Named `platform_reserve` ledger bucket, deferred — log + ops alert only in v1 |

## Slices & Progress

- [x] Slice 1 **[C]** — scaffold (`add-service` skill), compose/k8s/vars,
      `CREATE DATABASE payments`, schema migrations + views + procedures +
      sqlc, protos + buf generate — done (checkboxes back-filled 2026-07-28;
      the work landed across sessions without updating this list)
- [x] Slice 2 **[C]** — repo layer (all domains) + integration tests — done
- [x] Slice 3 **[C]** — fx/quote + beneficiaries + transfer create/read/
      timeline + guarded transitions (usecases + handlers) — done, grew
      past the plan: reverse quote (to_amount), beneficiary update,
      party-scoped proof reads (ListTransferFiles)
- [x] Slice 4 **[M]** — matching engine (offers, scoring, timeout/re-match)
      over the slice-1 procedures — done; wallets since merged to
      per-operator rows with accepts_usd/accepts_cdf (migration 0004)
- [x] Slice 5 **[M]** — ledger_request engine + holds (settle, release,
      capture); **[C]** deposit/withdrawal request flows + admin handlers on
      top — done; guarantee funding request now auto-opens on enrollment
      TOS accept; human refs (DEP-2026-NNNNNN) added
- [ ] Slice 6 **[C]** — commissions (accrual + batch logic, manual CLI) +
      disputes records; **[M]** dispute resolution money effects + the
      Tuesday periodic job — NOT STARTED: no commissions/disputes tables or
      domains exist yet. The remaining open slice.
- [x] Slice 7 — done in evolved form: River jobs (match, offer expiry,
      payment timeout, auto-confirm, sweep, notify) instead of a broker;
      proofs flow file→payment over gRPC (system passport,
      UpdateTransferStatus / UpdateLedgerRequestStatus) instead of
      `EventService.FileUploaded` + `consumed_events`; gateway routes
      (client/agent/OPS tiers) all mounted.
