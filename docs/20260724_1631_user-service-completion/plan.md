status: approved

# User service — completion

What is left on the user/identity side now that the gRPC surface (Auth /
User / Admin services, Cognito, OTP, account domain) is implemented. Mostly
wiring and hardening; the one real new domain is agent enrollment.

Companion docs: [platform-messaging-infra](../20260724_1631_platform-messaging-infra/plan.md)
(redis client), [payment-service](../20260724_1631_payment-service/plan.md)
(enrollment handshake). Predecessor:
`20260722_2101_user-service-implementation` (slices 1–5 done).

Ownership tags are proposals — re-assign at review. **[C]** = Claude,
**[M]** = Mahamadou.

## 1. Gateway wiring for the user domain **[C]**

The gateway serves only `/readiness`+`/liveness`; nothing dials the user
service yet.

- Dial `app/services/user/client` in `gateway/internal/api/api.go`
  (`api.Config` gains `UserServiceAddr`; connection closed in the returned
  `Close`).
- Route tiers on `/v1` (REST → gRPC, `protocodec.Proto` responses,
  `errs`-mapped errors — translate gRPC codes back to `errs.ErrCode`):

| Tier | Route | RPC |
|---|---|---|
| public | `POST /v1/auth/otp` | `Auth.SendOtp` |
| public | `POST /v1/auth/register` | `Auth.Register` |
| public | `POST /v1/auth/login` | `Auth.Login` |
| public | `POST /v1/auth/refresh` | `Auth.RefreshToken` |
| public | `POST /v1/auth/password` | `Auth.ChangePassword` |
| auth | `POST /v1/auth/logout` | `Auth.Logout` |
| auth | `GET /v1/me` · `PATCH /v1/me` | `User.Me` / `User.UpdateAccount` |
| OPS | `GET/PATCH/DELETE /v1/accounts*` | `Admin.*` |

- **Identity forwarding**: an authenticated route reads the passport from
  context (`core/passport`) and sets gRPC metadata `x-account-sub` (the
  contract `internal/sdk/identity` already reads) on the outgoing call.
- **OPS role check** (open decision, see §6): v1 resolves the caller's role
  by calling `User.Me` with the forwarded sub and caching the result per-sub
  for ≤60s. Cognito custom claims can replace this later without touching
  handlers.
- Runtime smoke (needs real Cognito env): register → login → `GET /v1/me`
  through the gateway; documented in the Progress section when done.

## 2. Redis OTP rate limiting **[C]**

Config exists (`OTP_SEND_COOLDOWN=60s`, `OTP_HOURLY_LIMIT=5`) but no redis
client is wired anywhere. Depends on `core/cache` from the
platform-messaging-infra doc.

- `SendOTP`: per-phone cooldown key (`SETNX otp:cd:<phone>` EX 60) and
  hourly counter (`INCR otp:hr:<phone>` EX 3600, reject > limit) **before**
  generating/sending — SMS cost is the resource being protected.
- Verify throttle: per-phone counter on failed verifies complementing the DB
  `attempts` cap (protects against many-OTP-rows abuse).
- Fail-open or fail-closed on redis outage: **fail-closed for send**
  (SMS costs money), **fail-open for verify** (DB attempts cap still guards).

## 3. Real OTP codes **[M or C — tiny]**

Replace the hardcoded `111111` in `usecase.getCodeAndHash` with
`crypto/rand` over the existing `codeSpace` constant. Keep a
`OTP_STATIC_CODE` env escape hatch for local dev (empty = random) so mobile
testing without SMS stays possible.

## 4. Agent enrollment workflow **[split — see below]**

Spec: admin-initiated (D4), TOS gate, guarantee funding, then activation.
The account side lives here; the money side lives in the payment service.

Flow (spec §agent-onboarding + G1–G5 screens):
1. **Admin invites** an account → `enrollment_request` row
   (`invited`), account role → `pending_agent` **[C: schema+repo, M: usecase]**
2. **Candidate accepts TOS** (G1, refusable) → `tos_accepted_at` stamped,
   request → `accepted`; decline → `declined`, role reverts to `user`. **[M]**
3. **Guarantee funding** happens in the payment service (FUNDING
   `ledger_request` of 20 USD carrying `enrollment_nk`). On its settlement
   the payment service enqueues a River `DeliverEvent` job
   (`payment.ledger_request.settled`) that calls an internal user-service
   RPC (`AdminService.ActivateAgent(enrollment_nk, event_id)` — idempotent
   on the event id), promoting: request → `activated`, role → `agent`.
   **[M — cross-service logic; C — RPC plumbing]**

Schema addition (migration `0002`): `enrollment_requests` — id, account_fk,
invited_by (account id), status enum (`invited/accepted/declined/activated/
cancelled`), tos_accepted_at, audit cols. AdminService gains
`InviteAgent` / `CancelEnrollment`; UserService gains `AcceptAgentTerms` /
`DeclineAgentTerms` (proto additions).

Implementation waits for the payment service's ledger_request to exist;
schema + protos can land earlier.

## 5. Notification preferences + contact info **[C]**

The infra doc places contact info + notification preferences in User (the
future Notification service reads them).

- Additive migration `0003`: `notification_preferences` on accounts —
  simplest v1: three booleans (`notify_sms`, `notify_push`, `notify_inapp`)
  as columns on `accounts`, defaults true. A separate table only if
  per-event-type granularity is confirmed needed.
- `UserService.UpdateAccount` + `Account` proto gain the fields; mappers and
  view updated.

## 6. Open decisions

| # | Question | Proposal |
|---|---|---|
| U1 | OPS role source at the gateway | v1: lookup via user client by sub + short cache; later: Cognito custom claim `role` set at SignUp/role-change (needs Cognito attribute write on `AdminUpdateAccount`) |
| U2 | LOGIN OTP creation is open (spec wanted it gated behind a password check) | Accept for v1 — the rate limits in §2 bound the abuse; revisit when OTP moves into a Cognito Lambda |
| U3 | Notification prefs shape | Booleans on accounts (see §5) |

## Slices & Progress

- [x] Slice A — gateway user routes + identity forwarding + smoke — done
      2026-07-24, jointly. Mechanism evolved past §1's sketch: instead of an
      `x-account-sub` header, the gateway's Auth middleware validates the
      JWT, rehydrates via `UserService.GetAccountBySub`, and forwards a
      **protobuf passport** (`core/client` interceptors, tolerant of
      anonymous/public calls); services rebuild it into context and read
      identity via `passport.FromContext`. Route tiers live in
      `api.Register` (public /v1/auth/*, authed /me + logout, ops
      /accounts* via `RequireRoles("ops","admin")`); gRPC statuses map to
      HTTP through `errs.FromGRPC`. Smoke-tested end to end against real
      Cognito: readiness 204, tier gates 403, `POST /v1/auth/otp` 200 with
      the code SMS-logged, business errors surface as 400 with clean
      bodies. Full register→login→me smoke pending a throwaway phone
      number (creates a real Cognito user).
- [ ] Slice A' — remaining polish **[M]**: per-sub caching of the
      rehydration lookup (TODO already in middleware/auth.go).
- [x] Slice B — done 2026-07-26. Per-PHONE limiter on `core/cache`
      (attackers rotate IPs; the phone is the resource — each send is an
      SMS with a cost): SetNX cooldown (60s) + fixed-window hourly cap
      (5/h) on SendOTP, cross-code verify budget (10/10min) on
      CheckAndConsumeOTP — the DB's per-code cap stops single-code
      brute force, the redis budget stops cycling fresh codes.
      Deliberately not a token bucket: cooldown + window compose into
      the same guarantee with existing primitives, and the window's
      boundary burst is bounded by the cooldown. FAILS OPEN (nil cache /
      redis outage → log + allow — cost protection must never lock
      logins; the DB cap holds the security floor). Env knobs:
      OTP_SEND_COOLDOWN, OTP_HOURLY_LIMIT, OTP_VERIFY_LIMIT,
      OTP_VERIFY_WINDOW. Tests vs live redis (cooldown, cap, per-phone
      isolation, verify budget, nil-cache fail-open); smoked through the
      gateway: second immediate send → 429.
- [ ] Slice C — crypto/rand codes + dev escape hatch **[M/C]** — parked
      2026-07-26: stays 111111 until an SMS provider that works well in
      the DRC is picked (M's call); `codeSpace` + the commented rand
      block wait in getCodeAndHash.
- [x] Slice D — agent enrollment: superseded by its own design doc, see
      [agent_enrollment.md](agent_enrollment.md) — status: done, all 4
      slices (including the payment linkage) implemented.
- [ ] Slice E — notification preferences **[C]**
- [ ] Slice F — notifications domain (web push + inbox): own design doc,
      see [notification.md](notification.md) (status: draft)
