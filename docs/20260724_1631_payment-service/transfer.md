status: done

# Transfers — quote, create, broadcast matching, proof-gated completion (payment service)

The last core piece. A client quotes a corridor, creates a transfer, the
matcher broadcasts offers to the best agents, the first accept wins and
reserves the agent's guarantee, proofs move the status exactly like the
ledger, and completion releases the hold. Money between client, agent and
recipient moves **outside** the platform in v1 — the only ledger effect of
a transfer is **reserve at accept / release at terminal**; no capture, no
ledger lines, no commission payout yet (commission is computed and stored,
settlement with the owner TBD).

Supersedes the matching/money sketches in [plan.md](plan.md) §2/§4 and the
two `*.outdated.ref.md` extracts where they differ.

Decisions settled at review 2026-07-25: broadcast offers (not the
sequential walk), hold in USD = the full client total including fee,
CDF→USD corridor not allowed, both sides stored on the transfer, operators
as an enum, agent self-service wallets, live FX provider with 5-min Redis
cache from day one.

Ownership: **[C]** everything below unless marked; **[M]** reviews the
matching score + the accept/release procedures before approval flips.

## 1. Corridors & money

Two currencies: **USD** and **CDF** (the drafts' "RDF" normalized). Three
corridors:

| Corridor | FX | Fee |
|---|---|---|
| USD → CDF | rate + 0.5% margin | 2.0% |
| USD → USD | none | 1.5% |
| CDF → CDF | none | 1.5% |

`CDF → USD` is **rejected at quote and at create** (settled 2026-07-25).
The rule, not the table: `hasExchange = (from_currency != to_currency)`,
`fee_pct = hasExchange ? 2.0 : 1.5`.

Amounts are BIGINT minor units with a per-currency exponent applied at the
edges: **USD = 2** (cents, as the ledger already does), **CDF = 0** (no
subunit in practice — 2 850 000 CDF is stored as 2850000). Rates are
NUMERIC(18,6). Fee percentages are config (env), stamped on the row at
create — never recomputed.

The client inputs the **source amount**; everything else is derived and
stored (T4: both sides on the row):

```
total_amount  = from_amount + fee_amount            (what the client pays, from_currency)
to_amount     = hasExchange ? from_amount × rate − margin : from_amount
hold_amount   = total_amount in USD minor units     (see §2 — always computed, even CDF→CDF)
```

## 2. FX — provider, cache, lock

`internal/integration/fxrate` **[C]**:

- `Provider` interface: `Rate(ctx, from, to Currency) (decimal, error)`.
- `HTTP` implementation: keyless public endpoint (default
  `https://open.er-api.com/v6/latest/USD`, `FX_PROVIDER_URL` override),
  parses the CDF quote.
- `Static` implementation: `FX_STATIC_RATE_USD_CDF` env — the **fallback**
  when the provider errors (log + serve the static rate, never fail a
  quote on a rate-API hiccup) and the local-dev default when no URL is
  configured.
- Read-through 5-min cache on `core/cache` (`cache:fx:usd:cdf`,
  `FX_CACHE_TTL` default 5m). The cache is the only rate most requests
  ever see.

**Lock semantics**: `GetQuote` is stateless — it reads the cached rate and
computes; nothing is persisted. The **lock happens at `CreateTransfer`**:
the rate is read again (same cache) and stamped on the row, immutable from
then on. Display-vs-locked drift is bounded by the 5-min TTL — accepted
for v1 (the quote screen and the confirm tap are seconds apart). The
Redis-side 15-min quote-lock machinery from the old plan is dropped.

Two stamped rates when they differ in role:
- `rate` — the FX applied to the client's money. NULL on same-currency
  corridors.
- `hold_amount` — USD minor units, always present: `total_amount`
  converted at the create-time USD:CDF rate when `from_currency = 'cdf'`,
  else `total_amount` as-is. A CDF→CDF transfer therefore still fetches
  the USD rate at create — for the hold only (settled 2026-07-25: held is
  USD; full sum including fee).

## 3. Operators & agent wallets

`operator` is a Postgres enum + mirrored Go/proto enum: `orange_money`,
`m_pesa`, `airtel_money`. No table (settled — three hardcoded values buy
nothing as rows). `GetAvailableOperators(from_currency, to_currency)`
returns the hardcoded slice (v1: all three support both currencies) and
rejects the CDF→USD pair.

Currency is a property of the **wallet**, not the operator (dollarized
market): `agent_wallets { agent_profile_fk, operator, currency, msisdn }`,
unique live `(agent_profile_fk, operator, currency)`, soft delete.
**Agent self-service** (settled): AgentService wallet RPCs below; admins
see them through the profile read.

The `msisdn` is why the wallet is more than a capability flag: after
accept, the client pays the agent **off-platform** (drawio: "send user
agent information") — the matched transfer surfaces the agent's wallet
matching `{from_operator, from_currency}` (operator + msisdn) to its
owner. That's the old plan's P2 decision, resolved here.

Wallet fit at matching: the agent must hold **both** ends —
`{from_operator, from_currency}` (receive the client's money) and
`{recipient_operator, to_currency}` (pay the recipient out).

## 4. Beneficiaries

`beneficiaries { client_nk, name, phone, operator, city }`, soft delete,
unique live `(client_nk, phone)`. Created **inline** by `CreateTransfer`
when no `beneficiary_id` is given (the draft's flow), listed and deleted
via their own RPCs. The transfer stores a **snapshot** of the recipient
(name/phone/operator/city columns) plus `beneficiary_fk` — editing a
beneficiary later never rewrites transfer history.

## 5. Schema — migration `0002_add_transfers.sql`

```sql
CREATE TYPE currency        AS ENUM ('usd', 'cdf');
CREATE TYPE operator        AS ENUM ('orange_money', 'm_pesa', 'airtel_money');
CREATE TYPE transfer_status AS ENUM
    ('pending_match', 'pending_agent_approval', 'pending_client_transfer',
     'pending_agent_transfer', 'pending_client_confirmation',
     'done', 'cancelled', 'expired', 'disputed');   -- disputed reserved, unused v1
CREATE TYPE offer_status    AS ENUM ('offered', 'accepted', 'declined', 'voided', 'expired');

CREATE SEQUENCE transfer_ref_seq;

CREATE TABLE transfers (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    reference          TEXT NOT NULL UNIQUE
        DEFAULT 'MKP-' || to_char(now(), 'YYYY') || '-' ||
                lpad(nextval('transfer_ref_seq')::text, 6, '0'),
    client_nk          UUID NOT NULL,              -- user-service account
    beneficiary_fk     UUID REFERENCES beneficiaries (id),
    -- recipient snapshot (immutable copy at create)
    recipient_name     TEXT NOT NULL,
    recipient_phone    TEXT NOT NULL,
    recipient_operator operator NOT NULL,
    recipient_city     TEXT,
    -- money, all stamped at create, never recomputed
    from_currency      currency NOT NULL,
    from_operator      operator NOT NULL,
    from_amount        BIGINT NOT NULL CHECK (from_amount > 0),
    to_currency        currency NOT NULL,
    to_amount          BIGINT NOT NULL,
    fee_pct            NUMERIC(5,2) NOT NULL,
    fee_amount         BIGINT NOT NULL,
    margin_amount      BIGINT NOT NULL DEFAULT 0,  -- 0 on same-currency
    total_amount       BIGINT NOT NULL,            -- from_amount + fee_amount
    rate               NUMERIC(18,6),              -- NULL = no FX (same currency)
    hold_amount        BIGINT NOT NULL,            -- USD minor units (§2)
    note               TEXT,                       -- client → agent
    status             transfer_status NOT NULL DEFAULT 'pending_match',
    agent_profile_fk   UUID REFERENCES agent_profiles (id),  -- set on accept
    match_round        INT NOT NULL DEFAULT 0,
    proof_client_nk    UUID,                       -- file_nk: client-paid proof
    proof_agent_nk     UUID,                       -- file_nk: payout proof
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at         TIMESTAMPTZ,
    CONSTRAINT transfers_corridor_ck CHECK
        (NOT (from_currency = 'cdf' AND to_currency = 'usd'))
);
CREATE INDEX transfers_client_idx ON transfers (client_nk, created_at DESC);
CREATE INDEX transfers_agent_idx  ON transfers (agent_profile_fk, created_at DESC)
    WHERE agent_profile_fk IS NOT NULL;
CREATE INDEX transfers_queue_idx  ON transfers (status, created_at)
    WHERE status IN ('pending_match', 'pending_agent_approval');

-- append-only timeline (no updated_at/deleted_at, no UPDATE procedure)
CREATE TABLE transfer_events (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    transfer_fk  UUID NOT NULL REFERENCES transfers (id),
    actor_nk     UUID,                     -- NULL = system/timer
    from_status  transfer_status NOT NULL,
    to_status    transfer_status NOT NULL,
    reason       TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX transfer_events_timeline_idx ON transfer_events (transfer_fk, created_at);

CREATE TABLE offers (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    transfer_fk      UUID NOT NULL REFERENCES transfers (id),
    agent_profile_fk UUID NOT NULL REFERENCES agent_profiles (id),
    match_round      INT NOT NULL,
    score            INT NOT NULL,
    status           offer_status NOT NULL DEFAULT 'offered',
    expires_at       TIMESTAMPTZ NOT NULL,
    decided_at       TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- one live offer per agent per transfer
CREATE UNIQUE INDEX offers_live_uq ON offers (transfer_fk, agent_profile_fk)
    WHERE status = 'offered';
CREATE INDEX offers_agent_inbox_idx ON offers (agent_profile_fk, status, expires_at);

-- + beneficiaries, agent_wallets (§3, §4)
```

No `holds` table: a transfer's reservation lives in
`transfers.hold_amount` + `agent_profiles.held`, and its exactly-once
release is guaranteed by the transfer **status guard** in the terminal
procedures (same trick as the ledger — the state machine IS the hold
ledger). The old plan's generic holds table is dropped.

Views (`payments_ext`): `transfers` (client/agent scoped reads),
`transfer_timeline`, `agent_offers` (live inbox join: offer + transfer
money fields), `agent_stats` (per profile, derived: `done_count`,
`active_load` = count in the three post-accept pendings, `success_rate` =
done / decided — nothing stored, teardown rule), `agent_wallets`,
`beneficiaries`, and `match_candidates` (the pre-filter join, §7).

## 6. Status machine

```
pending_match ──broadcast──► pending_agent_approval ──accept──► pending_client_transfer
      ▲                            │      │                            │
      └──── all offers dead ───────┘      │                     proof (client paid)
                                      cancel (client)                  ▼
 expired ◄── max rounds/deadline          │                 pending_agent_transfer
                                          ▼                            │
                                      cancelled ◄── timeout      proof (agent paid)
                                          ▲        (client                ▼
                                          │        never pays)  pending_client_confirmation
                                   ops cancel any                      │
                                   open state            confirm (client) / auto-confirm
                                   (releases hold                      ▼
                                    if placed)                        done
```

| From | Action | To | Actor | Effects (one statement / tx) |
|---|---|---|---|---|
| — | `CreateTransfer` | `pending_match` | client | row + rate/hold stamped + beneficiary upsert + **InsertTx match job** |
| `pending_match` | match run | `pending_agent_approval` | system | offer batch + InsertTx expiry job (at `expires_at`) + agent notifications |
| `pending_agent_approval` | `AcceptOffer` | `pending_client_transfer` | agent | THE race: offer accepted, siblings voided, `held += hold_amount` (guard `available ≥`), agent set, client notified **with the agent's payment details** (§3 msisdn) |
| `pending_agent_approval` | all offers declined/expired | `pending_match` | system | round++, re-enqueue match; → `expired` past the cap (release nothing — no hold yet) |
| `pending_match`/`pending_agent_approval` | `CancelTransfer` | `cancelled` | client | no hold yet; live offers voided |
| `pending_client_transfer` | proof `sender_proof` | `pending_agent_transfer` | system (file worker) | stamps `proof_client_nk`; agent notified |
| `pending_client_transfer` | payment timeout | `cancelled` | system | **release hold**; both parties notified |
| `pending_agent_transfer` | proof `agent_proof` | `pending_client_confirmation` | system (file worker) | stamps `proof_agent_nk`; client notified + auto-confirm job scheduled |
| `pending_client_confirmation` | `ConfirmReceipt` | `done` | client | **release hold**; agent notified |
| `pending_client_confirmation` | auto-confirm timeout | `done` | system | same as confirm (D11) |
| any open | ops cancel | `cancelled` | ops | release hold if placed; reason required |

Rules, mirroring the ledger:
- **Proof moves are system-only** (the file service's River worker →
  internal RPC), forward-only, and can never reach `done` — a proof upload
  must not release money. The client's confirm (or the timer) is the only
  path to `done`.
- Every transition writes a `transfer_events` row inside the same
  statement (CTE) — the timeline can never disagree with the status.
- `done`, `cancelled`, `expired` are terminal. `disputed` is reserved in
  the enum (enum additions need their own migration) but unreachable v1.
- Hold release appears in exactly three transitions (done, post-accept
  cancel/timeout) and each is status-guarded — released exactly once by
  construction, no `hold_released_at` bookkeeping needed.

### The accept procedure (the race, single statement)

```sql
WITH offer AS (
    UPDATE offers SET status = 'accepted', decided_at = now()
    WHERE id = p_offer_id AND agent_profile_fk = p_agent_profile
      AND status = 'offered' AND expires_at > now()
    RETURNING transfer_fk, agent_profile_fk
), t AS (
    UPDATE transfers tr SET status = 'pending_client_transfer',
        agent_profile_fk = offer.agent_profile_fk
    FROM offer
    WHERE tr.id = offer.transfer_fk AND tr.status = 'pending_agent_approval'
    RETURNING tr.id, tr.hold_amount, tr.status
), prof AS (
    UPDATE agent_profiles ap SET held = ap.held + t.hold_amount
    FROM t
    WHERE ap.id = p_agent_profile AND ap.status = 'active'
      AND ap.balance - ap.held >= t.hold_amount
    RETURNING ap.id
), voided AS (
    UPDATE offers o SET status = 'voided', decided_at = now()
    FROM t WHERE o.transfer_fk = t.id AND o.status = 'offered'
), ev AS (
    INSERT INTO transfer_events (transfer_fk, actor_nk, from_status, to_status)
    SELECT t.id, p_actor, 'pending_agent_approval', 'pending_client_transfer'
    FROM t, prof     -- prof in FROM: no reservation, no event, no rows
)
SELECT tr.* FROM transfers tr JOIN t ON t.id = tr.id JOIN prof ON true;
```

Zero rows = lost the race, offer expired, or insufficient available —
the caller reads the offer/profile to tell the agent which. The `prof`
CHECK-style guard means two agents can never both win and a winner can
never over-reserve; concurrent accepts serialize on the transfer row
UPDATE. Terminal procedures (`complete_transfer`, `cancel_transfer`)
follow the same CTE shape with `held −= hold_amount` where a hold exists.

## 7. Matching — broadcast (settled 2026-07-25)

All offers go out **at once** to the top `MATCH_MAX_AGENTS` (env, default
5) candidates; first accept wins, siblings are voided. No hold at offer
time — reservation happens at accept (which is what makes broadcast safe:
nothing is promised until someone commits).

**Pre-filter** — one query over the `match_candidates` view (draft 4.2
confirmed: one query works):

```sql
SELECT ... FROM agent_profiles ap
WHERE ap.status = 'active'
  AND ap.balance - ap.held >= :hold_amount            -- can cover the reservation
  AND ap.account_nk <> :client_nk                     -- self-deal guard
  AND EXISTS (wallet {from_operator, from_currency})
  AND EXISTS (wallet {recipient_operator, to_currency})
  AND NOT EXISTS (offer on this transfer with status = 'declined')  -- declined = out for good
ORDER BY ap.level DESC, available DESC
LIMIT :max_candidates;   -- MATCH_MAX_AGENTS × 3 headroom for scoring
```

**Score** (in Go, over the shortlist joined with `agent_stats`) — the
seven criteria, reweighted for what exists in v1: availability by
`active_load` (25), guarantee depth `available / (hold_amount+1)` ratio
ladder (20), wallet fit is a gate not a score (its 20 points fold into
guarantee depth and load), success rate with the neutral-8 cold start
(15), level (10), geo city match (5), load (5). Drop zeros, sort, take
`MATCH_MAX_AGENTS`, insert the offer batch + flip the status + notify in
one tx, `InsertTx` the expiry job scheduled at `expires_at`
(`OFFER_TTL` env, default 2m).

**River topology** (no 1-second cron — settled):
- `transfer.match` job — enqueued `InsertTx` by CreateTransfer and by
  every re-match path. Idempotent: no-ops unless the transfer is still
  `pending_match`.
- `transfer.offers_expire` job — scheduled at the batch's `expires_at`.
  Expires leftover `offered` rows; if none accepted → `pending_match`,
  `match_round++`, re-enqueue `transfer.match`. Past `MATCH_MAX_ROUNDS`
  (env, default 10) or `MATCH_DEADLINE` (default 30m from create) →
  `expired` + client notification.
- `transfer.payment_timeout` / `transfer.auto_confirm` jobs — scheduled
  when their clock starts (accept / agent proof); each no-ops if the
  state moved on.
- One slow periodic rescue sweep (every 5m) re-enqueues any
  `pending_match` transfer with no pending match job — belt and braces
  only.

Re-match policy: agents who **declined** are excluded for the transfer's
lifetime; agents who merely let the offer **expire** may be re-offered in
later rounds (they might have been away). Excessive-decline penalties:
deferred, `agent_stats` exposes `declined_count` so the score can consume
it later.

## 8. Proto — `proto/payment/v1`

New `transfer_service.proto`, `agent_service.proto`; shared enums join
`shared_message.proto`.

`TransferService` (client-facing; caller from passport):
```proto
rpc GetAvailableOperators(GetAvailableOperatorsRequest) returns (GetAvailableOperatorsResponse);
rpc GetQuote(GetQuoteRequest) returns (Quote);            // stateless (§2)
rpc CreateTransfer(CreateTransferRequest) returns (Transfer);
rpc GetTransfer(GetTransferRequest) returns (Transfer);   // owner or assigned agent
rpc ListTransfers(ListTransfersRequest) returns (ListTransfersResponse);   // mine; status filter
rpc ListTransferEvents(ListTransferEventsRequest) returns (ListTransferEventsResponse);  // timeline
rpc ConfirmReceipt(ConfirmReceiptRequest) returns (Transfer);
rpc CancelTransfer(CancelTransferRequest) returns (Transfer);   // pre-accept only
rpc ListBeneficiaries(ListBeneficiariesRequest) returns (ListBeneficiariesResponse);
rpc DeleteBeneficiary(DeleteBeneficiaryRequest) returns (google.protobuf.Empty);
rpc UpdateTransferStatus(UpdateTransferStatusRequest) returns (Transfer);  // SYSTEM-ONLY: proof moves, carries file_nk + purpose
```

`CreateTransferRequest` (the draft's shape, both sides explicit):
```proto
message CreateTransferRequest {
  Currency from_currency = 1;
  Operator from_operator = 2;
  int64    from_amount   = 3;               // minor units, source-denominated
  Currency to_currency   = 4;
  optional string beneficiary_id = 5;       // present → 6..9 ignored
  optional string recipient_name = 6;       // absent id → creates the beneficiary
  optional string recipient_phone = 7;
  optional Operator recipient_operator = 8;
  optional string recipient_city = 9;
  optional string note = 10;
}
```

The `Transfer` message carries an optional `AgentPaymentDetails
{operator, msisdn}` — populated only for the transfer's owner once
matched (the wallet matching `{from_operator, from_currency}`), so the
client knows where to send the money. Agent name/photo live in the user
service; v1 shows operator + msisdn only (the notification carries the
rest if needed later).

`AgentService` (agent-facing; profile resolved from passport):
```proto
rpc GetMyProfile(GetMyProfileRequest) returns (AgentProfile);
rpc ListMyOffers(ListMyOffersRequest) returns (ListMyOffersResponse);     // live inbox + money fields
rpc AcceptOffer(AcceptOfferRequest) returns (Transfer);
rpc DeclineOffer(DeclineOfferRequest) returns (Offer);
rpc ListMyTransfers(ListMyTransfersRequest) returns (ListTransfersResponse);
rpc ListMyWallets(ListMyWalletsRequest) returns (ListMyWalletsResponse);
rpc AddWallet(AddWalletRequest) returns (AgentWallet);        // self-service (settled)
rpc RemoveWallet(RemoveWalletRequest) returns (google.protobuf.Empty);
```

Ops surface rides on the existing `AdminService`: `ListAllTransfers`
(status filter), `CancelTransferAdmin` (reason required, releases hold).

Passport policy: TransferService = authenticated user (owner-scoped
reads); AgentService = role agent (`RequireRoles`), profile from
passport; `UpdateTransferStatus` = `RequireSystem` (mirrors the ledger's
UpdateStatus); admin ops = `RequireRoles(ops)`.

Notifications reuse the River notifier pattern (publisher `InsertTx` in
the same tx as the transition, worker → user `PushNotification` under
system passport): `transfer.offer_received` (agents),
`transfer.matched`, `transfer.client_paid`, `transfer.payout_done`,
`transfer.completed`, `transfer.expired`, `transfer.cancelled`.

The file service's `UploadWorker.Work` purpose-switch (the standing [M]
TODO) gains its first two arms: `sender_proof` / `agent_proof` →
`payment.TransferService.UpdateTransferStatus` under system passport,
exactly like the ledger's `deposit_proof` arm.

## 9. Business layout

```
internal/business/transfer/           types (Transfer, Event, Quote, enums), repo
internal/business/transfer/usecase/   quote (fx), create (rate lock + beneficiary
                                      upsert + match enqueue), transitions (confirm/
                                      cancel/proof), timers
internal/business/matching/           the engine: candidates query, score, offer
                                      batch, accept/decline, expiry/re-match  [C, M review]
internal/business/beneficiary/        types, repo (usecase folded into transfer's)
internal/business/agentwallet/        types, repo + self-service usecase
internal/integration/fxrate/          Provider, HTTP, Static, cached decorator (§2)
```

`agentprofile` gains nothing — `held` moves only through the transfer
procedures and the existing ledger ones.

## 10. Open decisions

| # | Question | Proposal |
|---|---|---|
| T1 | Offer TTL | `OFFER_TTL` env, default **2m** — short enough to walk 10 rounds inside the 30m deadline |
| T2 | Match cap | `MATCH_MAX_ROUNDS=10` AND `MATCH_DEADLINE=30m`, whichever first → `expired` |
| T3 | Client payment timeout (accept → proof) | `PAYMENT_TIMEOUT` env, default **24h** (settled 2026-07-25 — generous; the agent's hold sits reserved that whole time) → cancelled + release |
| T4 | Auto-confirm delay (D11) | `AUTO_CONFIRM_AFTER` env, default **24h** |
| T5 | Commission settlement with the platform/owner | Out of scope (M ↔ owner discussion); `fee_amount`/`margin_amount` stamped per transfer is the accrual record the future logic reads |
| T6 | CDF minor-unit exponent = 0 | Confirm — affects mobile formatting and the quote math |
| T7 | Agent payout timeout (client paid, agent silent) | v1: notify ops at `PAYOUT_ALERT_AFTER` (default 2h), **no auto-dispute** (disputes are unbuilt) |

## Slices & Progress

- [x] Slice 1 **[C]** — done 2026-07-25. Migration 0002 (5 enums, 5
      tables, ref sequence), 7 views, 15 procedures, sqlc, 4 repos
      (transfer, matching, beneficiary, agentwallet). The accept
      procedure gates EVERY effect on one guarded+locking CTE — a failed
      accept burns nothing (offer stays live). Race tests live: 6-way
      accept race → exactly 1 winner + 1 reservation, siblings voided;
      8-way complete race → hold released exactly once; timeout/admin
      cancels release, pre-accept cancel blocked post-accept; declined
      agents excluded from candidates forever, expired ones re-offerable;
      corridor CHECK kills cdf→usd; wallet-fit gate requires both ends;
      self-deal guard; timeline = 6 rows for the full happy path.
- [x] Slice 2 **[C]** — done 2026-07-25. fxrate integration (Static /
      HTTP-with-fallback / 5-min Redis read-through; unreachable redis
      degrades, never blocks startup), protos (Currency/Operator/
      TransferStatus/OfferStatus + Transfer/Offer/Beneficiary/
      AgentWallet/Quote, TransferService + AgentService), transfer
      usecase (stateless quote, integer-only money math — fees bps,
      rates micros, CDF holds ceil'd — create with rate lock +
      beneficiary upsert + InsertTx match job), TransferService
      handlers, grpcerr map. Unit tests pin the quote math corner cases
      (usd→usd no rate, usd→cdf margin, cdf→cdf hold conversion, all
      guards).
- [x] Slice 3 **[C, M review pending]** — done 2026-07-25. Matching
      engine: SQL pre-filter → Go scoring (availability 25 / guarantee
      depth 30 / success 15 w/ neutral-8 cold start / level 10 / geo 5 /
      load 15 — wallet-fit folded as a gate), broadcast round + expiry
      job scheduled in the same tx, ErrNoCandidates → worker snooze
      (30s), decline = immediate requeue when the round dies, caps →
      terminal expiry + owner notification. River workers (match,
      offers_expire, payment_timeout, auto_confirm, notify, 5-min rescue
      sweep as PeriodicJob) with the worker↔usecase loop closed before
      Start. Agent wallet self-service usecase + full AgentService
      handlers; AdminService gains ListAllTransfers +
      CancelTransferAdmin.
- [x] Slice 4 **[C]** — done 2026-07-25. Proof path: ApplyProof
      (system-only RPC; forward-only; replay-idempotent with read-back;
      payout proof arms auto-confirm in the same commit), Confirm /
      CancelByClient / CancelByAdmin / HandlePaymentTimeout /
      HandleAutoConfirm with notifications in-tx. File service
      UploadWorker completed: deposit_proof → ledger (now with real
      id/status/proof), sender_proof/agent_proof →
      UpdateTransferStatus; guard rejections complete the job (no
      poison retries). MokaClient Payment bundle gains Transfer + Agent
      clients. End-to-end usecase test against the live DB: create
      (money stamped, match scheduled) → round (offer + notify) →
      accept (hold + timeout clock + payment details on the owner read)
      → proofs (replay-safe) → confirm (done + release exactly once);
      lifecycle edges (empty pool snooze, decline requeue, round cap
      expiry, payment timeout with double-notify + idempotent stale
      timer). Service boots clean with all four gRPC services + workers.
- [x] Slice 5 **[C]** — done 2026-07-25. Gateway: payment connection
      (required) + `PAYMENT_GRPC_PORT`, new `agent` route tier
      (RequireRoles agent/pending_agent), 20 routes across three tiers
      (10 client transfer-flow, 8 agent, 2 ops), `param.Int32` helper.
      openapi extended (20 paths, 13 schemas, 3 new tags); request
      collection chains operators → quote → create (captures
      transfer_id) → read/timeline/confirm + the agent and ops flows.
      Compose: gateway gets payment env + depends_on; payment gets
      redis env. Smoked live: readiness 204, all three tiers reject
      anonymous calls.

**All five slices implemented; M reviewed 2026-07-25 — done.**
