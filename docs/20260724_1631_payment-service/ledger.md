status: done

# Ledger — agent profiles, ledger requests, immutable lines (payment service)

The ledger is the only mechanism that moves `agent_profiles.balance`. A
`ledger_request` is the mutable workflow object (proofs, approvals); its
settlement writes exactly one immutable `ledger_lines` row and mutates the
balance — atomically, serialized per profile by the row lock the UPDATE
itself takes. This volley covers requests + lines + the agent profile
lifecycle; holds/capture and commissions come later
([plan.md](plan.md) §3).

First consumer: the enrollment guarantee (user service `Accept` creates
the profile + the 20 USD funding request; `Approve` settles it and
activates the profile — see §6).

Ownership per the standing split: **[M]** the request engine usecase,
**[C]** schema/procedures/repo/protos/handlers.

## 1. Synchronization model

No explicit `FOR UPDATE` needed: settlement is **one SQL statement** built
from CTEs — the `UPDATE agent_profiles` acquires the row lock, concurrent
settlements on the same profile serialize behind it, and the loser of two
racing approves dies on the request-status guard (zero rows). Three layers
make double-settlement structurally impossible:

1. Status guard: `UPDATE ledger_requests … WHERE status =
   'pending_admin_approval'` — a request settles once.
2. `ledger_lines.ledger_request_fk UNIQUE` — even a bug can't write two
   lines for one request.
3. CHECK constraints (`balance ≥ 0`, `held ≥ 0`, `held ≤ balance`) — an
   overdraft aborts the whole statement whatever produced it.

**Typed references instead of idempotency keys (decided at review
2026-07-25)**: no event ever creates a ledger request, so a generic
producer key over-modeled the problem AND duplicated the enrollment
linkage. A request optionally references the entity it settles:
`reference_entity_type` (`enrollment` | `transfer`) + `reference_entity_id`,
moving together (CHECK), with a PARTIAL unique index — at most one
live-or-settled request per entity, a rejected one frees the slot (a
rejected guarantee deposit must be retryable). This is what makes the
one genuine retry path safe: `enrollment.Accept` commits locally before
calling CreateRequest, so the guarantee creation must be re-triggerable
(G2) without minting twins — create → 23505 → read by reference.
Unreferenced requests (agent-initiated funding/withdrawal) repeat
freely; double-submit is a UI concern (an optional client key can come
back later if it ever bites).

**Reservation at create (L1)**: an **outflow** request reserves its amount
on creation — `held += amount` in the same statement that inserts the
request, guarded by `available ≥ amount` (plus the guarantee floor for
withdrawals). Insufficient funds fail the *request*, not the approval:
"withdraw 100 with 20 on the books" is rejected on the spot, and two open
withdrawals can never promise the same money. Consequences:
- `RejectRequest` on an outflow releases the reservation (`held −= amount`).
- Settlement of an outflow is the crossover: `balance −= amount` **and**
  `held −= amount` together (the CHECK constraints stay green by
  construction — the amount was inside `held`).
- Inflow requests never touch `held`.
- An open outflow request IS a reservation; the future transfer `holds`
  table moves the same column for transfer auth/capture, each with
  exactly-once release via its own status machine.

## 2. Schema — migrations (payments DB)

```sql
CREATE TYPE agent_profile_status AS ENUM ('pending', 'active', 'suspended');
CREATE TYPE agent_level          AS ENUM ('n1', 'n2', 'n3', 'n4');

CREATE TABLE agent_profiles (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_nk      UUID NOT NULL,     -- user-service account, no cross-service FK
    status          agent_profile_status NOT NULL DEFAULT 'pending',
    level           agent_level NOT NULL DEFAULT 'n1',
    -- The denomination of balance/held/guarantee_floor (explicitness, not
    -- a transacting constraint: USD<->CDF sending directions are a
    -- transfer/payment-method concern and never touch the ledger).
    currency        TEXT NOT NULL DEFAULT 'USD',
    balance         BIGINT NOT NULL DEFAULT 0,     -- minor units
    held            BIGINT NOT NULL DEFAULT 0,     -- reserved by open outflow requests (+ transfer holds later)
    guarantee_floor BIGINT NOT NULL DEFAULT 2000,  -- 20 USD in minor units
    city            TEXT,                          -- geo matching input, nullable v1
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at      TIMESTAMPTZ,

    CONSTRAINT agent_profiles_balance_ck CHECK (balance >= 0),
    CONSTRAINT agent_profiles_held_ck    CHECK (held >= 0 AND held <= balance)
);
-- One profile per account among live rows.
CREATE UNIQUE INDEX agent_profiles_account_uq
    ON agent_profiles (account_nk) WHERE deleted_at IS NULL;
```

`available` (= `balance − held`) is **never stored** — derived in the view
only (teardown #6). Stats stay view-derived once transfers exist.

```sql
CREATE TYPE ledger_request_type AS ENUM
    ('funding', 'withdrawal', 'refund', 'deduction', 'commission');
CREATE TYPE ledger_request_status AS ENUM
    ('initiated', 'pending_agent_action', 'pending_admin_approval',
     'done', 'rejected', 'disputed');
-- Does the request increase or decrease the balance. Deliberately NOT
-- credit/debit: double-entry vocabulary flips meaning with the point of
-- view; inflow/outflow answers the only question this column has.
CREATE TYPE ledger_direction AS ENUM ('inflow', 'outflow');
CREATE TYPE ledger_reference_type AS ENUM ('enrollment', 'transfer');

CREATE TABLE ledger_requests (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_profile_fk UUID NOT NULL REFERENCES agent_profiles (id),
    type             ledger_request_type NOT NULL,
    -- Stored explicitly because refund can go either way; for the other
    -- types it is validated against the type at create.
    direction        ledger_direction NOT NULL,
    amount           BIGINT NOT NULL CHECK (amount > 0),  -- sign lives in direction
    status           ledger_request_status NOT NULL DEFAULT 'initiated',
    -- The entity this request settles (guarantee → enrollment, capture →
    -- transfer). Both NULL for free-standing requests (agent deposits,
    -- withdrawals). No cross-service/DB FK by design.
    reference_entity_type ledger_reference_type,
    reference_entity_id   UUID,
    proof_file_nk    UUID,          -- attached by UpdateStatus (file service)
    reason           TEXT,          -- human context: deduction cause, reject reason
    decided_by       UUID,          -- account_nk of the approving/rejecting admin
    decided_at       TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
    -- no deleted_at: a workflow object is history, never hidden
);
-- One live-or-settled request per referenced entity; rejected frees the
-- slot (same pattern as enrollment_requests_live_uq).
CREATE UNIQUE INDEX ledger_requests_reference_uq
    ON ledger_requests (reference_entity_type, reference_entity_id)
    WHERE reference_entity_id IS NOT NULL AND status <> 'rejected';
-- The pair moves together.
ALTER TABLE ledger_requests ADD CONSTRAINT ledger_requests_reference_ck
    CHECK ((reference_entity_type IS NULL) = (reference_entity_id IS NULL));
CREATE INDEX ledger_requests_worklist_idx
    ON ledger_requests (status, created_at DESC);
CREATE INDEX ledger_requests_profile_idx
    ON ledger_requests (agent_profile_fk, created_at DESC);
-- transfer_fk arrives with the transfers volley (additive migration).

CREATE TABLE ledger_lines (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_profile_fk  UUID NOT NULL REFERENCES agent_profiles (id),
    ledger_request_fk UUID NOT NULL UNIQUE REFERENCES ledger_requests (id),
    amount            BIGINT NOT NULL,   -- SIGNED: inflow +, outflow −
    balance_after     BIGINT NOT NULL,   -- the invariant auditors love
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
    -- append-only: no updated_at, no deleted_at, no UPDATE/DELETE procedure
);
CREATE INDEX ledger_lines_statement_idx
    ON ledger_lines (agent_profile_fk, created_at DESC);
```

## 3. Request state machine

**Creation lands with the next actor (decided at review 2026-07-25)**:
`add_ledger_request` takes the initial status (default `initiated`) —
an admin-created funding request starts at `pending_agent_action`
(waiting on the agent's deposit), an agent withdrawal at
`pending_admin_approval`. Nothing else would ever move those requests
off `initiated`: UpdateStatus belongs to the proof path. The procedure
only accepts OPEN statuses — creating in done/rejected/disputed is
impossible (it would bypass settlement). The landing status is the
usecase's derivation from (type, caller), never wire input; the repo
surface is `Add(ctx, status, direction, data)` — status and direction
are both engine decisions, not caller data.

```
 initiated ──► pending_agent_action ──► pending_admin_approval ──► done
     │                  │                        │                (writes the line)
     └──────────────────┴────────────────────────┴──► rejected
                                                 └──► disputed (reserved, transfers volley)
```

- `UpdateStatus` is **forward-only between the pendings** (and attaches
  `proof_file_nk`); it can never reach `done`/`rejected` — the file
  service reporting a proof must not be able to move money.
- `ApproveRequest`: only from `pending_admin_approval`. Settles (§1) and
  stamps `decided_by/at`. Idempotent for callers: approving a `done`
  request returns it unchanged (AlreadyDone short-circuit) — cross-service
  retries (enrollment!) depend on this.
- `RejectRequest`: from any non-terminal status, `reason` required.
- Type-specific guards live at **create** now (L1 — funds are reserved
  up front, approval never fails on money):
  - any outflow: `available ≥ amount`, and the profile must be `active`
    (a pending agent can fund, never withdraw).
  - `withdrawal`: additionally `available − amount ≥ guarantee_floor` —
    the guarantee is not withdrawable.
  - inflow: no funds guard; allowed on `pending` profiles too (the
    enrollment guarantee funds a not-yet-active profile).

### Procedures (all `LANGUAGE sql`, `payments_ext`)

`add_agent_profile` (23505 on the live-account index → profile exists),
`set_agent_profile_status(p_id, p_status)` (the one procedure under the
four intent RPCs), `delete_agent_profile` (soft; guard: balance = 0 AND
held = 0 — never delete money), `add_ledger_request` (validates
direction-vs-type; **CTE**: `held += amount` for outflows behind the
available/floor/active guards — zero rows = insufficient funds or
ineligible profile), `update_ledger_request_status` (forward-only guard
in the WHERE), `reject_ledger_request` (**CTE**: terminal flip + `held −=
amount` release for outflows), and `approve_ledger_request` — the CTE
settlement:

```sql
WITH req AS (
    UPDATE ledger_requests SET status = 'done',
        decided_by = p_decided_by, decided_at = now()
    WHERE id = p_id AND status = 'pending_admin_approval'
    RETURNING id, agent_profile_fk, direction, amount
), prof AS (
    UPDATE agent_profiles ap SET
        balance = ap.balance +
            CASE req.direction WHEN 'inflow' THEN req.amount ELSE -req.amount END,
        -- outflow settlement is the crossover: the reservation placed at
        -- create is consumed together with the balance
        held = ap.held -
            CASE req.direction WHEN 'outflow' THEN req.amount ELSE 0 END
    FROM req
    WHERE ap.id = req.agent_profile_fk
    RETURNING ap.id, ap.balance
)
INSERT INTO ledger_lines (agent_profile_fk, ledger_request_fk, amount, balance_after)
SELECT prof.id, req.id,
       CASE req.direction WHEN 'inflow' THEN req.amount ELSE -req.amount END,
       prof.balance
FROM req, prof
RETURNING *;
```

Zero rows = wrong id or not pending approval; the caller then reads the
request — `done` means a retry (fine), anything else is a real error.

## 4. Proto — `proto/payment/v1`

`LedgerService`:
```proto
rpc CreateRequest(CreateLedgerRequestRequest) returns (LedgerRequest);   // system/ops; optional typed reference
rpc GetRequest(GetLedgerRequestRequest) returns (LedgerRequest);         // by id
rpc GetRequestByReference(GetLedgerRequestByReferenceRequest) returns (LedgerRequest); // the enrollment/transfer lookup
rpc ListRequests(ListLedgerRequestsRequest) returns (ListLedgerRequestsResponse); // filters: status, type, agent_profile_id
rpc UpdateStatus(UpdateLedgerRequestStatusRequest) returns (LedgerRequest);  // system-only (file worker); forward-only; carries proof_file_nk
rpc ApproveRequest(ApproveLedgerRequestRequest) returns (LedgerRequest);  // ops/admin
rpc RejectRequest(RejectLedgerRequestRequest) returns (LedgerRequest);    // ops/admin; reason
rpc ListLines(ListLedgerLinesRequest) returns (ListLedgerLinesResponse);  // the statement, newest first
```

`AdminService` (payment.v1 — package-scoped, no clash with user.v1):
```proto
rpc CreateAgentProfile(CreateAgentProfileRequest) returns (AgentProfile);   // system (enrollment Accept) or ops
rpc GetAgentProfile(GetAgentProfileRequest) returns (AgentProfile);         // by id or account_nk
rpc ActivateAgentProfile(ActivateAgentProfileRequest) returns (AgentProfile);  // pending/suspended → active
rpc SuspendAgentProfile(SuspendAgentProfileRequest) returns (AgentProfile);    // active → suspended
rpc DeleteAgentProfile(DeleteAgentProfileRequest) returns (google.protobuf.Empty); // guard: zero balance & held
```

Intent-named lifecycle RPCs on purpose (agreed at review): each intent
carries its own authorization and guards, the RPC name is the audit
entry, and one `set_agent_profile_status` usecase/procedure serves all of
them underneath.

Passport policy: `CreateRequest`/`CreateAgentProfile` accept system or
ops; `UpdateStatus` system-only; approve/reject/suspend/activate/delete
ops/admin (`RequireRoles` never applies — these are internal RPCs, the
handlers check the forwarded passport role, mirroring the user service).

## 5. Business layout

```
internal/business/agentprofile/   types, usecase (lifecycle), repo
internal/business/ledger/         types (LedgerRequest, Line, enums), repo [C]
internal/business/ledger/usecase/ the engine: create (direction/type
                                  validation, reference replay), update-
                                  status, approve (settle + AlreadyDone
                                  short-circuit), reject  [M]
```

Both repos pgx-native on `pg.PgxPool` with `WithTx` (the enrollment/user
pattern verbatim).

## 6. The enrollment linkage (first consumer)

- `enrollment.Accept` (user service, **after** its local commit):
  1. `CreateAgentProfile(account_nk)` — idempotent via the live-account
     unique index (AlreadyExists = fine, retry).
  2. `CreateRequest(type=funding, direction=inflow, amount=2000,
     reference = (enrollment, enrollment id))` — idempotent via the
     partial reference index (AlreadyExists → read by reference, move on).
- `enrollment.Approve` (user service, **before** its local tx — remote
  calls never inside an open DB transaction):
  1. `GetLedgerRequestByReference(enrollment, enrollment id)` →
     `ApproveRequest(id)` (AlreadyDone = fine, retry).
  2. `ActivateAgentProfile(account_nk)`.
  3. Then the local enrollment tx (status + role). If it fails, the money
     side is already settled and every payment call is idempotent — the
     admin just retries the approve.

## 7. Open decisions

| # | Question | Proposal |
|---|---|---|
| L1 | ~~Withdrawal reservation~~ | **Decided at review**: outflow requests reserve at create (`held += amount` behind the available/floor guards); reject releases, settle crosses over. Approval never fails on funds. The transfer-capture path (holds volley) will create its deduction request pre-reserved — it consumes an existing hold, it must not reserve twice |
| L2 | ~~Currency mixing~~ | **Decided at review**: one profile per agent, balance denominated in USD (the guarantee currency); `currency` stored for explicitness only. USD↔CDF sending directions are handled by payment methods on transfers — the ledger never converts |
| L3 | `disputed` status | Reserved in the enum now (enum additions need their own migration); unused until transfers |
| L4 | Where refunds debit from | Platform-side buckets (plan.md P4) stay deferred; v1 refund requests target agent profiles only (agent_fault flow) |

## Slices & Progress

- [x] Slice 1 — migrations + views/procedures (CTE settlement) + sqlc +
      both repos + integration tests **[C]** — done 2026-07-25. Race
      tests prove the model live: 8 concurrent approves → exactly one
      settlement/line/balance move; 8 concurrent 300-withdrawals against
      1000 available → exactly 3 reservations win. Lifecycle tests cover
      the L1 semantics (reserve at create, floor guard 3001✗/3000✓,
      release on reject, crossover on approve), reference replay (was
      idempotency-key at slice time — superseded by the typed-reference
      review, migration pending),
      forward-only proof updates, funded-profile delete guard, and
      funding a pending profile (the enrollment case). One API note:
      NewAgentProfile.GuaranteeFloor is *int64 — 0 is a legitimate floor,
      nil means the 20 USD default.
- [x] Slice 2 — protos + agent-profile lifecycle usecase + handlers +
      passport policy **[C]** — done 2026-07-25. Full proto surface
      (both services — ledger RPCs renamed CreateLedgerRequest/… for buf
      lint); AdminService registered and smoked (anonymous →
      Unauthenticated); lifecycle usecase with idempotent
      activate/suspend (re-activate = no-op success, the
      enrollment-retry contract) and ErrInvalidTransition for illegal
      moves (suspend-pending). identity sdk: RequireSystem /
      RequireRoles / RequireSystemOrRoles — creation is system-or-ops,
      suspension/deletion human-only. LedgerService proto exists but is
      NOT registered — that happens with the [M] engine (slice 3).
- [x] Slice 3 — done 2026-07-25, jointly. Engine usecase **[M]** (role
      switch: agent = funding/withdrawal on THEIR OWN profile only,
      resolved from the passport; admin/ops = funding-with-enrollment-
      reference/refund; landing statuses per §3) with review fixes
      **[C]**: in-tx WithTx, duplicate update removed, nil-passport
      guards, default role branch, CreatedBy from passport, transition
      map trimmed to open states + procedure extended with the
      PAA→PAgA send-back. LedgerService handlers+mapper registered
      **[C]**; River notifier **[M]** (InsertTx publisher + worker →
      user PushNotification under system passport) wired with
      Start/Stop and river migrations in `go run . migrate` (both
      payment and file); Moka clients configured in payment (user) and
      file (user+payment) with compose overrides.
- [x] Slice 4 — done 2026-07-25 (with the enrollment linkage volley):
      Accept ensures the pending profile via system passport (idempotent),
      Approve settles the guarantee by enrollment reference + activates
      the profile under the approving admin's passport, before the local
      tx; ErrGuaranteeNotSettled blocks premature approval. Cross-service
      integration test against a live payment service.
- [x] Slice 5 — done 2026-07-25 (with the transfer gateway volley).
      Gateway ledger surface: agent tier (create own funding/withdrawal —
      profile id optional, engine resolves from the passport; list own
      requests; own statement) and ops tier (worklist + get + create +
      approve/reject + any agent's statement + agent-profile
      get/activate/suspend). Fixed an authz gap on the way: agent-scoped
      ListLedgerRequests/ListLedgerLines are now PINNED service-side to
      the caller's own profile — an agent can no longer read another
      agent's statement. openapi + request collection extended; smoked
      live (routes gated, readiness green).
