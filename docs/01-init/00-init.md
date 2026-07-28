# 00 — Prototype Teardown: Database, Endpoints & Flows

> **What this document is.** A reverse-engineered reference for the *prototype*
> found at `tmp/prototype/mokapay/`. That prototype was written by a
> non-developer partner as a proof-of-concept. It is **not** the codebase we are
> building — this repo (the Nx monorepo) is. This document exists so we can mine
> the prototype for domain knowledge (what the product actually does, what the
> money flow is, what tables and endpoints it needs) without inheriting its
> implementation.
>
> Read the **Flows** section for product intent, the **Database** and
> **Endpoints** sections for the concrete shape, and the **Findings** callouts
> for the traps we must *not* carry over. Nothing here is a design decision for
> our build yet — later docs (`01-…`, `02-…`) will make those.

---

## 1. What the product does

MOKA PAY moves money from the **diaspora** (senders in Europe/Africa) to
recipients in the **DRC / Congo**, settled through **human agents** on the
ground who hold mobile-money float and pay recipients out of it.

Three actors, three frontends:

| Actor | App (prototype port) | Does |
|---|---|---|
| **Sender (A)** | web PWA `:5174` | Creates a transfer, uploads proof of sending, confirms receipt |
| **Agent** | agent dashboard `:5175` | Gets matched to transfers, pays the recipient, uploads proof, funds a guarantee balance, earns commission |
| **OPS / Admin** | backoffice `:5176` | Creates agents, validates agent deposits, runs weekly commission payouts, supervises transactions |

The money never flows *through* the platform. The sender pays an agent (or the
platform's collection account) off-platform; the agent pays the recipient
off-platform; MOKA PAY orchestrates the match, holds the **proof**, and settles
**commission** to the agent weekly. The agent's **guarantee balance** is the
collateral that makes this trustable.

---

## 2. System topology (prototype)

Eight Go microservices behind a Gin **api-gateway**, talking **gRPC**
service-to-service, over a **shared** PostgreSQL, with Redis (FX cache /
rate-locks), RabbitMQ (declared, largely unused in code), and MinIO (proof
storage).

```
                         ┌─────────────┐   ┌─────────────┐   ┌─────────────┐
   web / agent / ops ───▶│ api-gateway │──▶│  8 services │◀─▶│ PostgreSQL  │
      (REST/JSON)        │   (Gin)     │gRPC│  (Go, gRPC) │   │  (shared)   │
                         └─────────────┘   └─────────────┘   └─────────────┘
                                                  │  Redis (FX) · MinIO (proofs) · RabbitMQ
```

| Service | Port | Responsibility | Has DB tables? |
|---|---|---|---|
| api-gateway | `4000` (HTTP) | REST → gRPC orchestration, auth/CORS middleware | no |
| auth-service | `4001` | OTP registration/login, JWT issue/validate | `users`, `otps`, `refresh_tokens` |
| transaction-service | `4002` | Transfer creation, status machine, sender confirm | `transactions` |
| agent-service | `4003` | Agent profiles, matching, guarantee deposits | `agents`, `guarantee_transactions`, `deposit_requests` |
| fx-service | `4004` | Rate aggregation, quote, rate-lock | **none** (in-memory + Redis) |
| document-service | `4005` | Proof upload to MinIO, cross-proof closure | `documents` |
| settlement-service | `4006` | Commission ledger, weekly payouts | `ledger_entries`, `commission_payments` |
| notification-service | `4007` | In-app + SMS notifications | `notifications` |

> **Finding — shared database anti-pattern.** All eight services read/write the
> *same* physical Postgres DB (`mokapay_db`). "Ownership" in the table above is
> logical (which service's code touches it), not physical isolation. agent-service
> even `SELECT … FOR UPDATE`s `agents` while settlement reads `agents`/`transactions`.
> Our build should give each service its own schema/DB boundary.

> **Finding — orphaned pieces.** `compliance-service` and `reporting-service`
> exist as empty shells (no `.go`). fx-service has a full gRPC contract but the
> gateway has **no FX client and no FX route** — the FX engine is unreachable
> from the outside even though every transaction depends on its math.

Infra credentials in the prototype are committed in plaintext
(`mokapay_secret_2026`, etc.) — placeholder dev values, not to be reused.

---

## 3. Database structure

Single PostgreSQL instance. `init.sql` creates `mokapay_db` (+ a `mokapay_test`
DB), enables `uuid-ossp` and `pgcrypto`, and defines a shared
`update_updated_at()` trigger function. UUID primary keys everywhere. Money is
`DECIMAL` (not float — good), but see the precision/labeling findings below.

### 3.1 Entity-relationship map

```
users (auth) ◄───────────────────────────────────────────────┐
  ▲  ▲  ▲  ▲                                                   │
  │  │  │  └── documents.uploaded_by            (FK)           │
  │  │  └───── transactions.sender_id           (FK)           │
  │  │  └───── transactions.agent_id            (FK) ⚠ →users  │
  │  └──────── refresh_tokens.user_id           (FK, CASCADE)  │
  └─────────── agents.user_id                   (FK, UNIQUE)   │
otps ─ (phone_number only, NO FK) ─────────────────────────────┘

agents (agent) ◄── deposit_requests.agent_id        (FK)
   ▲  ▲  ▲    ◄── guarantee_transactions.agent_id    (FK, CASCADE)
   │  │       ◄── ledger_entries.agent_id            (FK)
   │          ◄── commission_payments.agent_id       (FK)

transactions (tx) ◄── documents.transaction_id            (FK, CASCADE)
     ▲  ▲         ◄── guarantee_transactions.transaction_id (FK, nullable)
     │            ◄── ledger_entries.transaction_id        (FK)

LOGICAL-ONLY (no FK enforced):
  transactions.fx_lock_id ····▶ fx-service (no table at all)
  ledger_entries.payment_id ····▶ commission_payments.id
  deposit_requests.guarantee_transaction_id ····▶ guarantee_transactions.id
  notifications.user_id ····▶ users.id   (notifications table itself is UNMIGRATED)
```

### 3.2 Tables

**`users`** (auth) — identity for every role.
`id`, `phone_number` (unique, login id), `first_name`, `last_name`,
`role` CHECK`(USER,AGENT,OPS,ADMIN)` default `USER`, `kyc_level` `0-3`,
`status` CHECK`(PENDING,ACTIVE,SUSPENDED,BANNED)`, `preferred_language`
CHECK`(fr,en)`, `monthly_limit_usd`, `monthly_volume_usd` (denormalized running
aggregate), `created_at/updated_at` (trigger), `last_login_at`.

**`otps`** (auth) — bcrypt-hashed 6-digit codes. `phone_number` (no FK — OTP can
precede signup), `code_hash`, `purpose` CHECK`(REGISTER,LOGIN,RESET)`,
`expires_at` (+10 min), `used`, `attempts` (max-3 enforced in code only).

**`refresh_tokens`** (auth) — `user_id` FK CASCADE, `token_hash` (SHA-256, unique),
`expires_at` (+30 d), `revoked`, `device_info`.

**`transactions`** (tx) — the core money-transfer row.
`id`, `reference` (unique, `MKP-YYYY-NNNNNN`), `sender_id` FK→users,
`sender_phone` (denormalized), `recipient_phone`, `recipient_name`,
`agent_id` FK→**users** ⚠, amounts (`amount_sent`, `fee_amount`, `fee_percentage`,
`margin_amount`, `exchange_rate`, `amount_received`), `currency_sent/received`
(free text), `has_exchange`, `fx_lock_id` (no FK/table), `status`
CHECK`(PENDING,ASSIGNED,IN_PROGRESS,PROOF_SUBMITTED,COMPLETED,DISPUTED,CANCELLED,EXPIRED)`,
`payment_channel_in/out` (free text, no CHECK), inline proof columns
(`proof_sender_url/at`, `proof_agent_url/at`), `confirmed_by_sender(_at)`,
`agent_commission`, `commission_paid(_at)`, `note`, `created_from_ip`,
lifecycle timestamps (`assigned_at`, `completed_at`, `expires_at` = +60 min).

**`agents`** (agent) — an agent is a `users` row with role AGENT plus this profile.
`id` (distinct from user id), `user_id` FK→users (unique, CASCADE), `level` `1-4`,
`status` CHECK`(ACTIVE,INACTIVE,SUSPENDED)`, `city`, `country` default `CD`,
`wallets TEXT[]` (channel strings, unconstrained), `guarantee_balance_usd`,
`total_deposited/withdrawn_usd`, `max_transaction_usd`, plus denormalized stats
(`total_transactions`, `successful_transactions`, `success_rate`,
`active_transactions`, `months_active`, `total_commissions_earned_usd`),
`has_insurance` (+provider/expiry), timestamps.

**`guarantee_transactions`** (agent) — running ledger of guarantee movements.
`agent_id` FK→agents CASCADE, `type` CHECK`(DEPOSIT,WITHDRAWAL,DEDUCTION,REFUND)`,
`amount_usd`, `balance_after_usd` (snapshot), `transaction_id` FK→transactions
(nullable, for DEDUCTION/REFUND), `note`.

**`documents`** (document) — proof screenshots in MinIO.
`transaction_id` FK CASCADE, `type` CHECK`(SENDER_PROOF,AGENT_PROOF,RECIPIENT_PROOF)`,
`uploaded_by` FK→users, `storage_key/url`, `bucket_name` default `mokapay-preuves`,
`file_name`, `file_size` (32-bit int, ~2 GB cap), `content_type`, `file_hash`
(SHA-256), `signature` (HMAC-SHA256), `status` CHECK`(PENDING,VALID,REJECTED)`, `note`.

**`ledger_entries`** (settlement) — append-only accounting, one row per closed tx.
`transaction_id` FK, `agent_id` FK→agents, `transaction_amount_usd`,
`transaction_currency`, `total_fee_usd`, `margin_usd`, `agent_share_usd` (60%),
`platform_share_usd` (30%), `guarantee_fund_usd` (10%), `platform_total_usd`
(30% + full margin), `accounting_week` (`YYYY-WW` string), `payment_status`
CHECK`(PENDING,PAID)`, `payment_id` (logical ref, no FK).

**`commission_payments`** (settlement) — weekly (Tuesday) payout batches.
`agent_id` FK→agents, `week_start/end` (DATE), `accounting_week` (`YYYY-WW`),
`transaction_count`, `total_amount_usd`, `status` CHECK`(CALCULATED,PAID,FAILED)`,
`payment_channel` (no CHECK), `payment_reference`, `calculated_at`, `paid_at`.

**`deposit_requests`** (agent) — agent-declared guarantee top-ups, OPS-validated.
`id`, `reference` (`DEP-YYYY-NNNNN`, unique), `agent_id` FK→agents,
`amount_usd` (nullable until Complete, CHECK `>0`), `payment_channel` (nullable,
no CHECK), `agent_reference`, `proof_url`, `status`
CHECK`(INITIATED,PENDING_VALIDATION,VALIDATED,REJECTED,DISPUTED)`,
`validation_method` CHECK`(MANUAL,AUTO)`, `validated_by` FK→users, `validated_at`,
`guarantee_transaction_id` (logical ref, no FK), dispute fields, `ops_note`,
declared-origin fields (`sender_phone`, `sender_account`, `sender_full_name`),
`created_at/updated_at` (**column exists but no trigger**).

**`notifications`** (notification) — **⚠ has NO migration file.** Schema exists
only implicitly in `notification_repository.go`: `id`, `user_id`, `type`,
`title`, `body`, `reference`, `link`, `read`, `sms_sent`, `push_sent`,
`created_at`. No FKs, CHECKs, or indexes anywhere.

**No tables:** fx-service (rates are in-memory `float64` + Redis), compliance,
reporting, api-gateway.

### 3.3 Database findings to fix in our build

1. **`notifications` is unmigrated** — table only exists in repository code.
2. **`agent_id` means two different things.** `transactions.agent_id` → `users(id)`,
   but every other `agent_id` (guarantee/ledger/payments/deposits) → `agents(id)`.
   Joining a transaction to its ledger row needs a hop through `agents.user_id`.
   This is a live bug source; pick one identity and keep it.
3. **Missing FKs** on real relationships: `transactions.fx_lock_id`,
   `ledger_entries.payment_id`, `deposit_requests.guarantee_transaction_id`, all
   of `notifications`.
4. **Triple bookkeeping of commission** — on `transactions`
   (`agent_commission`/`commission_paid`), on `ledger_entries`
   (`agent_share_usd`/`payment_status`), and in `commission_payments`. These drift.
5. **Proof stored twice** — inline on `transactions.proof_*_url` *and* in the
   `documents` table (with hash/signature). Two sources of truth.
6. **Denormalized aggregates as stored columns** (`users.monthly_volume_usd`, all
   the `agents.*` stats) require app-side maintenance and desync.
7. **Status/enum columns are `VARCHAR` + CHECK, not native `ENUM`** — adding a
   value means dropping/recreating a constraint (migration `008` does exactly this).
   Our SQL rules mandate `CREATE TYPE … AS ENUM`.
8. **Channel/currency columns are unconstrained free text** (`payment_channel_in/out`,
   `deposit_requests.payment_channel`, `currency_sent/received`, `agents.wallets[]`).
9. **`*_usd` columns hold non-USD values** — amounts are stored in the *sent*
   currency (EUR, ZAR…) with no normalization. The "USD" accounting is fiction.
10. **`updated_at` triggers are inconsistent** — only `users`, `transactions`,
    `agents` get them; `deposit_requests.updated_at` has no trigger.

---

## 4. Endpoints

### 4.1 HTTP REST — api-gateway (Gin)

Base prefix `/api/v1`, port `4000`. Global middleware: `Logger → Recovery →
CORS`. Auth is delegated to auth-service over gRPC (`ValidateToken`), which
injects `user_id`, `role`, `phone_number` into the request context.

Three access tiers:
- **Public** — no auth.
- **Authenticated** (`RequireAuth`) — any valid JWT (USER or AGENT).
- **OPS** (`RequireAuth` + `RequireRole("OPS","ADMIN")`).

**Public**

| Method | Path | → gRPC | Notes |
|---|---|---|---|
| GET | `/health` | — | static |
| POST | `/auth/send-otp` | `Auth.SendOTP` | registration step 1 |
| POST | `/auth/register` | `Auth.VerifyOTPAndRegister` | returns tokens + user (201) |
| POST | `/auth/login/send-otp` | `Auth.LoginSendOTP` | login step 1 |
| POST | `/auth/login/verify` | `Auth.LoginVerifyOTP` | returns tokens + user |
| POST | `/auth/validate` | `Auth.ValidateToken` | reads bearer from header (misfiled as public) |

**Authenticated** (any valid token)

| Method | Path | → gRPC | Notes |
|---|---|---|---|
| POST | `/transactions` | `Tx.CreateTransaction` (+ async `Agent.MatchAgent`, `Tx.UpdateStatus`, notif) | sender from JWT; auto-matches an agent in a goroutine |
| GET | `/transactions` | `Tx.ListTransactions` | role-branched (agent vs user) |
| GET | `/transactions/:id` | `Tx.GetTransaction` | ⚠ no ownership check |
| POST | `/transactions/:id/confirm` | `Tx.ConfirmBySender` | → COMPLETED + commission |
| PATCH | `/transactions/:id/status` | `Tx.UpdateStatus` | ⚠ arbitrary status, no guard |
| POST | `/transactions/:id/proof/sender` | `Doc.UploadProof(SENDER_PROOF)` | multipart or base64, 10 MB |
| POST | `/transactions/:id/proof/agent` | `Doc.UploadProof(AGENT_PROOF)` (+ flips tx IN_PROGRESS, notif) | |
| POST | `/transactions/:id/proof/recipient` | `Doc.UploadProof(RECIPIENT_PROOF)` | |
| GET | `/transactions/:id/proofs` | `Doc.GetProofs` | ⚠ no ownership check |
| GET | `/agents/me` | `Agent.GetAgent(user_id)` | |
| POST | `/agents/me/deposit` | `Agent.DepositGuarantee` | legacy direct credit, **no OPS approval** |
| POST | `/agents/me/withdraw` | `Agent.WithdrawGuarantee` | |
| GET | `/agents/me/deposit-channels` | `Agent.GetDepositChannels` | static list |
| POST | `/agents/me/deposit-requests/initiate` | `Agent.InitiateDepositRequest` | |
| POST | `/agents/me/deposit-requests/:id/complete` | `Agent.CompleteDepositRequest` | ⚠ no ownership check |
| GET | `/agents/me/deposit-requests` | `Agent.ListMyDepositRequests` | |
| POST | `/agents/me/deposit-requests/:id/dispute` | `Agent.OpenDepositDispute` | ⚠ no ownership check |
| GET | `/agents/me/commissions` | `Settlement.GetAgentCommissionSummary` | |
| GET | `/notifications` | `Notif.List` | |
| GET | `/notifications/unread-count` | `Notif.CountUnread` | |
| POST | `/notifications/:id/read` | `Notif.MarkAsRead` | |
| POST | `/notifications/read-all` | `Notif.MarkAllAsRead` | |

**OPS** (`OPS`/`ADMIN` only)

| Method | Path | → gRPC |
|---|---|---|
| POST | `/agents` | `Agent.CreateAgent` |
| GET | `/agents` / `/agents/:id` | `Agent.ListAgents` / `GetAgent` |
| GET | `/settlement/summary` | `Settlement.GetPlatformSummary` |
| POST | `/settlement/calculate` | `Settlement.CalculateWeeklyCommissions` |
| POST | `/settlement/pay/:id` | `Settlement.MarkAsPaid` |
| GET | `/ops/deposit-requests/pending` | `Agent.ListPendingDepositRequests` |
| POST | `/ops/deposit-requests/:id/validate` | `Agent.ValidateDepositRequest` (credits balance) |
| POST | `/ops/deposit-requests/:id/reject` | `Agent.RejectDepositRequest` |
| GET | `/ops/transactions` | `Tx.ListAllTransactions` |

### 4.2 gRPC service contracts (summary)

Full field lists are in the `.proto` files; the RPC surface per service:

- **AuthService** — `SendOTP`, `VerifyOTPAndRegister`, `LoginSendOTP`,
  `LoginVerifyOTP`, `RefreshToken`*, `ValidateToken`, `GetUserProfile`*.
  (* defined but no gateway route — there is **no refresh-token endpoint** despite
  refresh tokens being issued.)
- **TransactionService** — `CreateTransaction`, `GetTransaction`,
  `ListTransactions`, `ListAllTransactions`, `UpdateStatus`, `ConfirmBySender`.
  `UpdateStatusRequest.new_status` is a **free-form string** — no enum.
- **AgentService** — `CreateAgent`, `GetAgent` (id overloaded: accepts agent-id
  *or* user-id), `ListAgents`, `DepositGuarantee`, `WithdrawGuarantee`,
  `DeductGuarantee`, `UpdateStats`, `MatchAgent`, `GetDepositChannels`, and the
  deposit-request lifecycle (`Initiate`/`Complete`/`ListMy`/`ListPending`/
  `Validate`/`Reject`/`OpenDispute`).
- **FxService** — `GetRate`, `LockRate`, `ValidateLock`, `Calculate`.
  **Not wired into the gateway at all.**
- **DocumentService** — `UploadProof`, `GetProofs`, `CrossProofs` (internal).
  Closure only tracks sender+agent proofs; `RECIPIENT_PROOF` doesn't affect closure.
- **SettlementService** — `RecordCommission` (internal), `CalculateWeeklyCommissions`,
  `MarkAsPaid`, `GetAgentCommissionSummary`, `GetPlatformSummary`.
- **NotificationService** — `Send` (internal, best-effort), `MarkAsRead`,
  `MarkAllAsRead`, `List`, `CountUnread`. Types: `TRANSACTION_CREATED`,
  `AGENT_NEW_ASSIGNMENT`, `TRANSACTION_COMPLETED`, `TRANSACTION_DISPUTED`,
  `AGENT_SENDER_PAID`, `AGENT_COMMISSION_PAID`, `OPS_DEPOSIT_REQUEST`, `OPS_AML_ALERT`.

### 4.3 Endpoint findings to fix in our build

1. **Rate limiting is dead code.** `RateLimit()` (100 req/IP/min) is implemented
   but **never registered** in `main.go`. OTP send/register/login have zero
   brute-force protection.
2. **No ownership checks** on `GET/PATCH /transactions/:id`,
   `/transactions/:id/proofs`, `/transactions/:id/confirm`, and the
   `/agents/me/deposit-requests/:id/*` routes — any authenticated user can act on
   any object by guessing an id. `PATCH /transactions/:id/status` lets any user
   set any status string on any transaction.
3. **Two guarantee-deposit flows coexist** — the legacy instant `DepositGuarantee`
   (no OPS approval) and the newer OPS-validated deposit-request flow. The legacy
   one bypasses the control entirely.
4. **`MarkAsPaid` id confusion** — the payout notification passes a *payment id*
   where an *agent id* is expected, so the post-payout notification usually
   resolves wrong/empty.
5. **FX engine orphaned** — the fee/rate math every transaction depends on is
   reached only internally by transaction-service, never exposed for quoting.

---

## 5. End-to-end flows

### 5.1 Registration & login (OTP, passwordless)

1. `send-otp` → auth-service generates a 6-digit code with `crypto/rand`,
   **bcrypt-hashes** it into `otps`, returns plaintext for SMS (+10 min expiry).
2. `register` / `login/verify` → `FindValidOTP` (unexpired, unused, `attempts<3`),
   `bcrypt.Compare`, then `MarkOTPUsed`. Registration creates the `users` row;
   login rejects `SUSPENDED`/`BANNED`.
3. `GenerateTokenPair` → access JWT (HS256, claims `user_id/phone/role`) +
   refresh JWT whose **SHA-256 hash** is stored in `refresh_tokens`.
   `ValidateToken` pins HS256 (blocks `alg:none`).

> There is **no identity-KYC flow**. `kyc_level` exists on `users` but nothing
> sets it; the "document" service handles transaction *proofs*, not ID documents.
> `compliance-service` is empty. **JWT access token lifetime is configured in
> _days_** despite comments calling it short-lived.

### 5.2 Creating a transfer (quote → lock → persist)

1. Gateway `POST /transactions` → transaction-service `Create` validates amount
   & currencies.
2. **FX quote** — `fxClient.Calculate`: `feePercentage` = **1.5%** same-currency /
   **2.0%** cross-currency; `feeAmount = amount × pct`. If cross-currency:
   `GetRate` → `exchangeRate`, `marginAmount = netAmount × 0.5%`,
   `amountReceived = (netAmount − margin) × rate`.
3. **Rate lock** — `LockRate` stores a 16-byte hex lock in Redis
   (`fx:lock:<id>`, 15-min TTL), returns `LockId` + `LockedRate`.
4. **Commission pre-computed** — `agentCommission = feeAmount × 0.60`, saved on the row.
5. Insert `transactions`: reference `MKP-YYYY-NNNNNN`, `status='PENDING'`,
   `expires_at = +60 min`.
6. Fire `TRANSACTION_CREATED` notification; kick off agent matching (async).

### 5.3 Agent matching & assignment

1. Gateway (background goroutine) calls `Agent.MatchAgent` with **`City` hardcoded
   to "Kinshasa"**.
2. agent-service pulls `ACTIVE` agents whose `max_transaction_usd ≥ amount` and
   whose `wallets[]` covers the in-channel, then **scores each 0–100** over 7
   weighted criteria: availability (25), guarantee (20), wallet match (20),
   success rate (15), level (10), geo (5), current load (5). Highest wins.
3. If the winner isn't the sender (self-deal guard), gateway sets the transaction
   to `ASSIGNED` (writes `agent_id`, `assigned_at`) and notifies the agent
   (`AGENT_NEW_ASSIGNMENT`). Match failure → transaction stays `PENDING` (logged only).

### 5.4 Agent guarantee deposit (funding the collateral)

Agents must hold **≥ 20 USD** guarantee balance to be `ACTIVE`.
`INITIATED → PENDING_VALIDATION → {VALIDATED | REJECTED | DISPUTED}`:

1. `initiate` → creates a `deposit_requests` row with reference `DEP-YYYY-NNNNN`;
   **balance untouched**.
2. Agent pays MOKA PAY's collection account off-platform, then `complete` with the
   amount, channel, declared sender coords, and proof → status `PENDING_VALIDATION`
   (still no credit).
3. OPS `validate` → `ValidateAndCredit` runs **one atomic SQL tx**: `SELECT … FOR
   UPDATE` on the request (idempotent — returns early if already `VALIDATED`) and
   on `agents.guarantee_balance_usd`; credits balance; recomputes
   `max_transaction_usd`; flips the agent to `ACTIVE` when ≥ 20; writes a
   `guarantee_transactions(DEPOSIT)` row; marks the request `VALIDATED`. All-or-nothing.

### 5.5 Executing the transfer & closing it

1. Agent pays the recipient off-platform, uploads `AGENT_PROOF` (flips tx to
   `IN_PROGRESS`). Sender uploads `SENDER_PROOF`.
2. document-service `tryCrossProofs`: when **both** sender & agent proofs exist,
   sets tx `PROOF_SUBMITTED` (only from `ASSIGNED`/`IN_PROGRESS`). Files are
   SHA-256 hashed + HMAC-signed in MinIO; frontend reads 1-hour presigned URLs.
3. Sender `confirm` → `ConfirmBySender` (allowed from
   `PROOF_SUBMITTED`/`IN_PROGRESS`/`ASSIGNED`) sets `status='COMPLETED'`,
   `completed_at`, then **best-effort** records commission.

Status machine actually implemented:
```
PENDING ─match→ ASSIGNED ─→ IN_PROGRESS ─both proofs→ PROOF_SUBMITTED ─confirm→ COMPLETED
```
`UpdateStatus` is an unguarded switch — arbitrary strings accepted. `CANCELLED`/
`EXPIRED`/`DISPUTED` exist in the CHECK constraint but are never driven; the
60-min `expires_at` is **set but never enforced**.

### 5.6 Settlement (the 60/30/10 rule)

Commission splits the **fee**:
- **Agent = fee × 60%**, **Platform = fee × 30%**, **Guarantee fund = fee × 10%**.
- The **0.5% FX margin goes 100% to the platform**, on top:
  `platform_total = fee×0.30 + margin`.

1. On `ConfirmBySender`, transaction-service calls `Settlement.RecordCommission`
   (best-effort, 5 s), which writes one `ledger_entries` row (`accounting_week`
   = ISO `YYYY-WW`) and then flips `transactions.commission_paid=true`.
2. Weekly (accounting week Tue→Mon, payout each **Tuesday**): OPS
   `CalculateWeeklyCommissions` sums pending `agent_share_usd` per agent into a
   `commission_payments` batch and links the ledger rows.
3. OPS `MarkAsPaid` records the mobile-money reference → `AGENT_COMMISSION_PAID`
   notification.

### 5.7 Notifications

All sends are fire-and-forget (`SendBestEffort` goroutine — never blocks or fails
the main flow). Every send persists an in-app row in `notifications`. `SMS` in
dev just logs; in prod POSTs to **AfricasTalking**. `PUSH` is a stub; the frontend
polls `/notifications`.

### 5.8 Rattrapage (reconciliation scripts) — cautionary tale

Three **independent, hand-written** backfill scripts (`rattrapage.go`,
`rattrapage/main.go`, `rattrapage_commissions.py`) find
`COMPLETED AND commission_paid=false` transactions and call `RecordCommission`,
then `UPDATE … commission_paid=true`. They:
- **hardcode a single `agent_id` (`458c7c78-…`) for every transaction** — so all
  backfilled commission is misattributed to one agent;
- send **mismatched payloads** (one omits amount, another omits reference);
- carry **inconsistent hardcoded DB passwords**; the Python one builds SQL via
  f-string interpolation and shells out to `docker exec psql` / `grpcurl`.

### 5.9 Flow findings to fix in our build

1. **Ledger is not idempotent.** `ledger_entries` has no unique constraint on
   `transaction_id`; the only double-count guard is a *separate* `commission_paid`
   update after the gRPC call. A crash in between — or a rattrapage run racing the
   live path — double-books commission. Make the ledger self-idempotent.
2. **FX margin is double-charged.** `AppliedRate` already subtracts the 0.5%
   margin, yet `Calculate` subtracts `marginAmount` again — the client effectively
   loses ~0.99%, not 0.5%.
3. **"USD" accounting is mixed-currency.** `*_usd` fields are filled with sent-currency
   values; nothing normalizes to USD.
4. **agent-id vs user-id mismatch** propagates from matching (returns user-id) into
   `transactions.agent_id` and then into `RecordCommission`.
5. **Production FX returns base rates.** Every real source fetch is a `TODO` stub;
   in `production` the aggregator just returns hardcoded base rates through the
   weighting/margin math.
6. **No transition guards, no expiry job.** `UpdateStatus` accepts anything;
   `expires_at` is never acted on; there is no cancel/refund/failed path.

---

## 6. Takeaways for our build

The prototype is a **complete, coherent product spec expressed as code** — the
domain model (sender / agent / OPS, guarantee-backed off-platform settlement,
proof-crossing, 60/30/10 weekly commission) is sound and worth keeping. What must
**not** carry over is the implementation: shared DB, `VARCHAR`+CHECK pseudo-enums,
duplicated/denormalized money columns, dual identity for agents, dead security
middleware, missing ownership checks, a non-idempotent ledger, and hand-rolled
reconciliation scripts.

When we build the real services in this repo, follow the workspace rules
(`.claude/rules/sql.md`, `coding-style.md`): native `ENUM` types, views-for-reads /
procedures-for-writes, one source of truth per fact, per-service schema isolation,
value semantics, and an idempotent settlement ledger. Subsequent design docs will
turn each flow above into a concrete service design.
