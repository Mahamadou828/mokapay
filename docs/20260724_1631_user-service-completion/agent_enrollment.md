status: done

# Agent enrollment (user service)

Admin-initiated promotion of a user to agent (spec §agent-onboarding, D4,
G1–G5): invite → TOS acceptance → guarantee funding (payment service,
TODO) → admin approval. The enrollment lives in the user service; the
money side (agent profile, 20 USD guarantee FUNDING ledger_request) is the
payment service's and is stubbed with TODOs until its doc is approved.

v1 simplification vs [plan.md](plan.md) §4: activation is a **manual
admin approval** (`ApproveEnrollment`), not an event consumed from the
payment service. When payment lands, its `ledger_request.settled` event
can notify the admin (or auto-flag the row) — the RPC stays.

A `pending_agent` behaves exactly like a `user`: nothing anywhere gates on
role `user`, so the candidate keeps full app access throughout.

Ownership proposals: **[C]** = Claude, **[M]** = Mahamadou.

## 1. State machine

```
                    invite (admin)
                          │
                          ▼
        ┌─────────── invited ───────────┐
        │                 │             │
 decline (user)    accept (user)   cancel (admin)
        │                 │             │
        ▼                 ▼             │
    declined          accepted ─────────┤
                          │             │
                   approve (admin)      │
                          ▼             ▼
                      approved      cancelled
```

| From | Action | To | Actor | Side effects (one tx) |
|---|---|---|---|---|
| — | `PromoteUserToAgent` | `invited` | ops/admin | role → `pending_agent`; notify candidate (`enrollment.invited`) |
| `invited` | `AcceptEnrollment` | `accepted` | candidate | stamp `tos_version` + `tos_accepted_at`; TODO(M): payment `CreateProfile` + guarantee `CreateRequest` |
| `invited` | `DeclineEnrollment` | `declined` | candidate | role → `user` |
| `invited`/`accepted` | `CancelEnrollment` | `cancelled` | ops/admin | role → `user`; stamp `decided_by/at` |
| `accepted` | `ApproveEnrollment` | `approved` | ops/admin | role → `agent`; stamp `decided_by/at`; notify candidate (`enrollment.approved`) |

Rules the procedures enforce (status-guarded UPDATEs, `SETOF` — zero rows
= lost the race or wrong state):
- No decline after accept — post-TOS exit goes through an admin cancel.
- Approve only from `accepted`, and only if the account is still `active`.
- Invite eligibility: account `active` + role `user` (not agent/ops/banned).
- `declined`, `cancelled`, `approved` are terminal. Re-inviting after a
  terminal state creates a **new row** (history preserved).
- Escape hatch: `AdminService.UpdateAccount` can still hard-set roles; the
  enrollment flow doesn't fight moderation — it just stops matching its
  guards (documented, not "fixed").

## 2. Schema — migration `0003_add_enrollments.sql` **[C]**

```sql
CREATE TYPE enrollment_status AS ENUM
    ('invited', 'accepted', 'declined', 'approved', 'cancelled');

CREATE TABLE enrollment_requests (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_fk      UUID NOT NULL REFERENCES accounts (id),  -- the candidate
    invited_by      UUID NOT NULL REFERENCES accounts (id),  -- ops/admin caller
    status          enrollment_status NOT NULL DEFAULT 'invited',
    tos_version     TEXT,            -- which terms were accepted
    tos_accepted_at TIMESTAMPTZ,     -- stamped on accept
    decided_by      UUID REFERENCES accounts (id),  -- approve/cancel admin
    decided_at      TIMESTAMPTZ,     -- terminal-transition stamp
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at      TIMESTAMPTZ
);

-- One live enrollment per account; a double invite is a clean 23505.
CREATE UNIQUE INDEX enrollment_requests_live_uq
    ON enrollment_requests (account_fk)
    WHERE status IN ('invited', 'accepted') AND deleted_at IS NULL;

CREATE INDEX enrollment_requests_status_idx
    ON enrollment_requests (status, created_at DESC)
    WHERE deleted_at IS NULL;        -- the ops dashboard listing
```

**Views** (`users_ext`): `enrollment_requests` (live rows).
**Procedures**: `add_enrollment(p_account, p_invited_by)`,
`accept_enrollment(p_id, p_account, p_tos_version)` (guard: `status =
'invited' AND account_fk = p_account`), `decline_enrollment(p_id,
p_account)` (same guard), `cancel_enrollment(p_id, p_decided_by)` (guard:
`status IN ('invited','accepted')`), `approve_enrollment(p_id,
p_decided_by)` (guard: `status = 'accepted'`). All SETOF; role flips
happen in Go inside the same tx, not in the procedures (the auth repo owns
the accounts table).

## 3. Business layer — `internal/business/enrollment/` **[C plumbing, M payment calls]**

Own domain package next to `auth` and `notification`: types at the root
(`Enrollment`, `Status`, `EnrollmentFilter`), `usecase/`, `repo/`.

Cross-domain dependencies as interfaces on the usecase:
- `AccountStore` — `GetAccount`, role update, `WithTx` (satisfied by the
  auth repo; **the auth repo gains `WithTx(tx pgx.Tx)`** — first consumer).
- `Notifier` — `Push(ctx, NewNotification) (Notification, error)`
  (satisfied by the notification usecase directly — same service, no
  self-gRPC; push failures are already non-fatal there).

Every transition that touches a role runs in one `BeginPgx` transaction:
enrollment procedure + `update_account` role flip commit or roll back
together. The notification row rides outside the tx (its own semantics —
best-effort, and Push already owns its persistence).

`AcceptEnrollment` calls, after the tx commits:
```go
// TODO(Mahamadou): payment.AgentService.CreateProfile — inactive profile
// TODO(Mahamadou): payment.LedgerService.CreateRequest — 20 USD guarantee
// FUNDING, carrying enrollment id as enrollment_nk
```

## 4. Proto **[C]**

`AdminService` gains:
```proto
rpc PromoteUserToAgent(PromoteUserToAgentRequest) returns (Enrollment);  // account_id
rpc ApproveEnrollment(ApproveEnrollmentRequest) returns (Enrollment);    // enrollment id
rpc CancelEnrollment(CancelEnrollmentRequest) returns (Enrollment);      // enrollment id
rpc ListEnrollments(ListEnrollmentsRequest) returns (ListEnrollmentsResponse);  // optional status filter
```

`UserService` gains (self-service — caller from the passport):
```proto
rpc GetMyEnrollment(GetMyEnrollmentRequest) returns (Enrollment);        // latest; NotFound if never invited
rpc AcceptEnrollment(AcceptEnrollmentRequest) returns (Enrollment);      // enrollment id + tos_version
rpc DeclineEnrollment(DeclineEnrollmentRequest) returns (Enrollment);    // enrollment id
```

`Enrollment` message: id, account_id, invited_by, status enum,
tos_version?, tos_accepted_at?, decided_by?, decided_at?, created_at,
updated_at.

## 5. Gateway routes **[C]**

| Tier | Route | RPC |
|---|---|---|
| authed | `GET /v1/enrollment` | `GetMyEnrollment` |
| authed | `POST /v1/enrollment/accept` | `AcceptEnrollment` (id + tos_version in body) |
| authed | `POST /v1/enrollment/decline` | `DeclineEnrollment` |
| ops | `GET /v1/enrollments?status=` | `ListEnrollments` |
| ops | `POST /v1/accounts/{id}/promote` | `PromoteUserToAgent` (path owns the account id) |
| ops | `POST /v1/enrollments/{id}/approve` | `ApproveEnrollment` |
| ops | `POST /v1/enrollments/{id}/cancel` | `CancelEnrollment` |

openapi + service.request.http extended.

## 6. Open decisions

| # | Question | Proposal |
|---|---|---|
| E1 | Invitation expiry (invited > N days) | Skip in v1 — admins cancel manually; a River cron can sweep later |
| E2 | TOS version source | Config env (`TOS_VERSION=2026-07-25`) validated against the accept payload, so the backend knows which terms the client showed |
| E3 | Notify ops when a candidate accepts | Skip in v1 — the ops dashboard (`ListEnrollments?status=accepted`) is the worklist |
| E4 | Decline-after-accept | Not allowed; admin cancel is the exit. Revisit if support tickets say otherwise |

## Slices & Progress

- [x] Slice 1 — migration 0003 + views/procedures + sqlc + repo (+ auth
      repo `WithTx`) + integration tests **[C]** — done 2026-07-25. Full
      state machine tested live: happy path (invite → accept w/ TOS stamp
      → approve w/ decision stamp), double-invite 23505 →
      ErrEnrollmentPending, foreign accept rejected, decline-after-accept
      and approve-from-invited impossible, terminal states frozen,
      re-invite after decline/approve works, cancel from both live
      states, status-filtered listing.
- [x] Slice 2 — protos + enrollment usecase (role flips in-tx,
      Notifier wiring, payment TODOs) + handlers **[C]** — done
      2026-07-25. `TOS_VERSION` env (default 1.0) validates accepts (E2).
      Usecase integration tests prove the atomicity live: an approve of a
      suspended account rolls back BOTH the status transition and the
      role flip; invite/decline/cancel flip roles in-tx;
      invited/approved notifications fire through the notification
      usecase. Runtime smoke: all 7 RPCs registered, anonymous call →
      Unauthenticated. Payment TODOs sit in Accept, after the commit.
- [x] Slice 3 — gateway routes + openapi/request collection **[C]** —
      done 2026-07-25. Seven routes (3 authed candidate-side, 4 ops);
      openapi at 23 paths / 21 schemas; request collection chains
      promote → accept → approve via a captured enrollment_id global.
      Smoked live: all three tiers gate correctly without a token.
- [x] Slice 4 — done 2026-07-25. The model shifted with the ledger
      engine: the guarantee funding request is ADMIN-created (payment
      policy), so Accept only ensures the (pending) agent profile exists
      (system passport, idempotent), and Approve — before its local tx —
      settles the guarantee by enrollment reference and activates the
      profile under the approving admin's own passport (their identity
      becomes the ledger decided_by). ErrGuaranteeNotSettled
      (FailedPrecondition) blocks approval until the funding request is
      pending admin approval. Direct MokaClient calls, no adapter (M's
      preference). Cross-service integration test drives the whole flow
      against a live payment service (gated on TEST_PAYMENT_GRPC_PORT).
