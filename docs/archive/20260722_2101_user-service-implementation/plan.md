status: done

# User service — design

The first real service of the platform: **identity and authentication**.
Owns accounts (client / agent / admin), roles, account lifecycle, and the
phone + password + OTP auth flow. Per the infra spec
([04-infrastructure.md](../01-init/01-project-specs/04-infrastructure.md)) it
gets its own Postgres database and uses Redis for OTP / login rate limiting.

Sources: project spec ([01-project-spec.md](../01-init/01-project-spec.md) —
§Actors, §Account model), prototype teardown
([00-init.md](../01-init/00-init.md) — auth-service tables & flows, findings).

## Decisions taken (2026-07-23)

| Topic | Decision |
|---|---|
| Auth provider | **In-service auth** (no Keycloak). Keycloak has no native phone-OTP flow (needs a community Java SPI + custom image + own DB + status/role sync); the infra doc already assigned auth to this service. |
| Login model | **Phone + password, then an OTP screen** (SMS second factor). ⚠ Supersedes the spec's "passwordless via OTP" line — `01-project-spec.md` §Account model needs a matching amendment. Rationale: a password anchored to the account gives a natural base for future fingerprint login / other 2FA. |
| SMS delivery | Provider selection is deferred (DRC routes need real testing). v1 defines the `internal/integration/sms` **interface only** and ships a mock/logging implementation. |
| Token validation | Gateway validates by **calling `AuthService.ValidateToken`** on every request (no local JWT verification). Signing key never leaves the service (HS256 secret), and status checks are live — a suspended account dies on its next request, not at token expiry. If the hop shows in latency, add a short gateway-side cache. |
| API naming | `Me` (not `GetMe`), self-update is `UpdateAccount` on `UserService`; all admin RPCs live in a separate **`AdminService`**. |

## Decisions revised (2026-07-24 — Cognito pivot)

Supersedes the auth-provider and token rows above; the rest of the 2026-07-23
decisions stand.

| Topic | Decision |
|---|---|
| Auth provider | **AWS Cognito** owns credentials and sessions (rolling our own auth is not production-grade). `core/aws/cognito.go` wraps AdminCreateUser / AdminSetUserPassword / ADMIN_USER_PASSWORD_AUTH / refresh / global sign-out / AdminDelete. The OTP flow stays in-service until an SMS provider exists, then moves into a Cognito Lambda trigger. |
| Our DB | No `password_hash` column, no `refresh_tokens` table — Cognito holds both. `accounts` gains `cognito_sub` (nullable TEXT, unique among live provisioned rows); the seeded OPS row stays NULL until its Cognito user is provisioned. |
| Token validation | Gateway validates **locally against the Cognito JWKS** (`core/passport` + gateway `middleware.Auth`) — the `ValidateToken` RPC is gone. Account status is enforced in the auth usecase at login and refresh (suspended/banned cannot mint tokens); mid-session revocation happens via Cognito global sign-out + short access-token TTLs. |
| Login shape | Single call: `Login(phone, password, code)` — the client gets a LOGIN OTP via `SendOtp`, then sends password + code together (`VerifyLogin` removed). Register/refresh/change-password follow the same collapse; `AuthResponse` carries tokens only (profile via `UserService.Me`). |
| Register consistency | Cognito sign-up and the DB insert are a dual write: on `AddAccount` failure the usecase compensates with `AdminDelete` so the phone is never orphaned in Cognito. |

## Scope

**In v1 of this service:**
- `accounts` — the single identity row for every human (spec: one Account,
  role-based capabilities, one role per account — D1).
- Auth: register (OTP-verified phone + password), two-step login
  (password → OTP), refresh/logout, forgot-password reset, change-password,
  token validation for the gateway.
- Own-profile management (`Me` / `UpdateAccount`).
- Admin: list/get accounts, moderate (suspend / reactivate / ban), role change
  (the primitive that agent enrollment will drive later).

**Explicitly out (later slices / other services):**
- Agent enrollment workflow (`enrollment_request`, TOS gate) — next slice on
  this service + Payments; this design only provides the `role` primitive
  (`user → pending_agent → agent`).
- Agent profile / balance — **Payments** service (infra doc).
- Saved beneficiaries — **Payments** (infra doc §Recipients).
- KYC (spec D6 deferred), notification preferences (infra doc open point —
  additive migration later), real SMS delivery (interface + mock only).

---

## 1. Database

Service database: **`users`** (avoid `user` — reserved word in Postgres).
Layout per `.claude/rules/sql.md`: migrations + `users_ext` schema with
views-for-reads / procedures-for-writes, sqlc for codegen.

### Enums

```sql
CREATE TYPE account_role     AS ENUM ('user', 'pending_agent', 'agent', 'ops', 'admin');
CREATE TYPE account_status   AS ENUM ('pending', 'active', 'suspended', 'banned');
CREATE TYPE account_language AS ENUM ('fr', 'en');
CREATE TYPE otp_purpose      AS ENUM ('register', 'login', 'reset');
```

- `pending_agent` is the interim enrollment role from the spec (§Agent
  enrollment). One role per account (D1) → a promotion **replaces** `user`.
- `ops` and `admin` are kept as two values, treated as one tier for v1 (D2).
- Self-registered accounts are born `active` (the phone is OTP-verified before
  the row exists). `pending` is reserved for future non-self-service creation.
- `reset` is the forgot-password purpose — passwords make a reset flow
  mandatory from day one.

### Tables (migration `0001_create_accounts.sql`)

```sql
CREATE TABLE accounts (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    phone_number       TEXT NOT NULL,              -- E.164, normalized at the API edge
    cognito_sub        TEXT,                       -- Cognito user id; NULL until provisioned
    first_name         TEXT NOT NULL,
    last_name          TEXT NOT NULL,
    email              TEXT,
    preferred_language account_language NOT NULL DEFAULT 'fr',
    role               account_role     NOT NULL DEFAULT 'user',
    status             account_status   NOT NULL DEFAULT 'active',
    last_login_at      TIMESTAMPTZ,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at         TIMESTAMPTZ
);

-- Phone is the login identity: unique among live rows only (soft delete frees it).
CREATE UNIQUE INDEX accounts_phone_number_uq
    ON accounts (phone_number) WHERE deleted_at IS NULL;

-- One account per Cognito user, among live provisioned rows only.
CREATE UNIQUE INDEX accounts_cognito_sub_uq
    ON accounts (cognito_sub) WHERE deleted_at IS NULL AND cognito_sub IS NOT NULL;
```

No credential material is stored — passwords and sessions live in Cognito.

```sql
CREATE TABLE otps (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    phone_number TEXT NOT NULL,          -- no FK: a register OTP precedes the account
    code_hash    TEXT NOT NULL,          -- bcrypt of the 6-digit code
    purpose      otp_purpose NOT NULL,
    attempts     INT  NOT NULL DEFAULT 0,
    expires_at   TIMESTAMPTZ NOT NULL,   -- created_at + OTP_TTL (10 min)
    consumed_at  TIMESTAMPTZ,            -- set once, atomically, on successful verify
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at   TIMESTAMPTZ
);

-- The only OTP lookup: latest live code for (phone, purpose).
CREATE INDEX otps_lookup_idx
    ON otps (phone_number, purpose, created_at DESC)
    WHERE consumed_at IS NULL AND deleted_at IS NULL;
```

(The `refresh_tokens` table from the original design was dropped in the
Cognito pivot — Cognito owns sessions.)

Prototype findings addressed: native `ENUM`s instead of `VARCHAR`+CHECK (#7),
`updated_at` trigger on **every** table (#10), `consumed_at`/`revoked_at`
timestamps instead of booleans, no denormalized aggregates (no
`monthly_volume_usd` — limits are D6, deferred).

### Views (`internal/db/view/`, schema `users_ext`)

- `users_ext.accounts` — all business columns (incl. `cognito_sub`),
  `WHERE deleted_at IS NULL`.
- `users_ext.otps` — live codes only: `WHERE consumed_at IS NULL AND
  expires_at > now() AND deleted_at IS NULL`. The verify path reads the newest
  row per (phone, purpose).

### Procedures (`internal/db/procedure/`, schema `users_ext`)

Standard triple per entity, plus the auth-specific guarded writes:

| Procedure | Notes |
|---|---|
| `add_account` | Required: phone, first/last name. Optional: email, language, role, status, cognito_sub. Returns the row. |
| `update_account` | `COALESCE` semantics; covers profile fields **and** `role`, `status`, `last_login_at`, `cognito_sub` (authorization is the API layer's job). |
| `delete_account` | Soft delete. |
| `add_otp` | Inserts a new code row. |
| `consume_otp(p_id)` | `UPDATE … SET consumed_at = now() WHERE id = p_id AND consumed_at IS NULL AND expires_at > now() RETURNING *` — **atomic**: two racing verifies can both pass the hash check in Go, only one row update wins. Empty result = lost the race / expired → reject. |
| `bump_otp_attempts(p_id)` | `attempts + 1`; the usecase rejects over the attempts cap **before** comparing. |
| `refresh_otp(p_id, p_code_hash, p_expires_at)` | Resend: swaps in the new code hash, extends expiry, counts the resend against `attempts`. Only touches live rows. |

### sqlc queries (`internal/db/query/accounts.sql`, `auth.sql`)

Reads against the views, writes through the procedures. `ListAccounts` is a
single query with `sqlc.narg(role)` / `sqlc.narg(status)` /
`sqlc.narg(phone_number)` optional filters (one query, NULL disables — per the
workspace rule). `GetValidOtp(phone, purpose)` = newest row from the otps
view. Account lookups: `GetAccount(id)`, `GetAccountByPhone`,
`GetAccountBySub` (refresh/logout flows resolve the caller by Cognito sub).

---

## 2. Auth design

### Flows

All flows normalize the phone to E.164 (`nyaruka/phonenumbers`, region CD)
before touching the DB or Cognito — the OTP rows and the Cognito username
both live under the normalized form. OTP verification is centralized in
`CheckAndConsumeOTP` (newest live code → attempts cap → HMAC compare →
atomic consume; a failed compare bumps `attempts`).

**Registration** (phone → OTP → profile + password → account):
1. `SendOtp(phone, REGISTER)` — issues a 6-digit code (HMAC-SHA256 hash into
   `otps`, plaintext to the SMS mock). A live code is resent via
   `refresh_otp` (new hash, new expiry, counts against attempts).
2. `Register(phone, code, password, first_name, last_name, language, email?)`
   — `CheckAndConsumeOTP(REGISTER)` → Cognito `SignUp` (AdminCreateUser +
   permanent password, phone pre-verified) → `add_account` with the returned
   `sub` (**on failure: compensating `AdminDelete`**) → Cognito login →
   token pair.

**Login — one call, OTP as second factor:**
`Login(phone, password, code)` — account lookup by phone, **reject
`suspended`/`banned`** (checked before the OTP so a blocked user doesn't burn
a code) → `CheckAndConsumeOTP(LOGIN)` → Cognito ADMIN_USER_PASSWORD_AUTH →
stamp `last_login_at` (non-fatal) → token pair. The code is consumed before
the password check reaches Cognito: a wrong password costs the user a new
OTP round-trip (accepted trade-off — no replay window on a verified code).

**Refresh**: `RefreshToken(refresh_token, sub)` — account lookup by
`cognito_sub`, re-check status (suspended/banned cannot mint new tokens) →
Cognito REFRESH_TOKEN_AUTH (SECRET_HASH derived from the sub). Note: Cognito
returns a new refresh token only when rotation is enabled on the app client —
otherwise the client keeps the old one.

**Logout**: Cognito `AdminUserGlobalSignOut` (by phone) — revokes every
refresh token; already-issued access tokens live until expiry, so keep the
access TTL short.

**Forgot / change password**: `SendOtp(phone, RESET)` →
`ChangePassword(phone, code, new_password)` — `CheckAndConsumeOTP(RESET)` →
Cognito `AdminSetUserPassword` (permanent) + global sign-out. A sign-out
failure after a successful password write is logged and tolerated
(`aws.ErrFailToLogOut`), not surfaced as a request failure.

### Tokens & validation

- **Tokens are Cognito's** — RS256 JWTs signed by the user pool. The service
  never mints or signs anything.
- **Gateway validation is local**: `middleware.Auth` verifies signature
  (JWKS via `keyfunc`), expiry, issuer, `token_use=access` and client id
  through `core/aws.ValidateToken`, then puts the claims into the request
  context via `core/passport`. No per-request RPC.
- **Revocation model**: status checks run at login/refresh in the usecase;
  moderation + password change trigger Cognito global sign-out. Between those
  events an access token stays valid — TTL is the bound.

### Rate limiting (Redis)

In the auth usecase, per the infra doc (Redis lives with this service; dev
reuses the compose `redis`):
- `SendOtp`: cooldown 60 s per phone, max 5/hour per phone.
- `Login`: per-phone attempt throttle (password brute-force guard).
- OTP verify: the DB `attempts` counter (max 3) is the real guard; Redis adds
  a per-phone verify throttle.
- Gateway-level per-IP limiting is a separate, later concern (prototype
  finding #1: never leave it as dead code).

### Identity propagation

The gateway's auth middleware validates the JWT locally and forwards the
verified identity (Cognito `sub`, from the passport claims) as gRPC metadata
to downstream services. Handlers read the caller from context and resolve the
account via `GetAccountBySub` — `Me` takes no id argument. Admin RPCs
re-check the role (defense in depth; gateway route tiers are the first gate).

---

## 3. gRPC API (`proto/user/v1/`)

Four files: `shared_message.proto` (entity + enums), `auth_service.proto`,
`user_service.proto`, `admin_service.proto`.

### shared_message.proto

```proto
package user.v1;

enum AccountRole {   // values mirror the DB enum
  ACCOUNT_ROLE_UNSPECIFIED = 0;
  ACCOUNT_ROLE_USER = 1;
  ACCOUNT_ROLE_PENDING_AGENT = 2;
  ACCOUNT_ROLE_AGENT = 3;
  ACCOUNT_ROLE_OPS = 4;
  ACCOUNT_ROLE_ADMIN = 5;
}
enum AccountStatus { … PENDING / ACTIVE / SUSPENDED / BANNED … }
enum Language      { … FR / EN … }
enum OtpPurpose    { … REGISTER / LOGIN / RESET … }

message Account {
  string id = 1;
  string phone_number = 2;
  string first_name = 3;
  string last_name = 4;
  optional string email = 5;
  Language preferred_language = 6;
  AccountRole role = 7;
  AccountStatus status = 8;
  google.protobuf.Timestamp last_login_at = 9;   // absent if never
  google.protobuf.Timestamp created_at = 10;
  google.protobuf.Timestamp updated_at = 11;
}
// No password field anywhere — hashes never cross the wire.
```

### auth_service.proto

```proto
service AuthService {
  rpc SendOtp(SendOtpRequest) returns (SendOtpResponse);          // {phone, purpose} → {expires_in_seconds}; NEVER echoes the code
  rpc Register(RegisterRequest) returns (AuthResponse);            // phone, code, password, first_name, last_name, language, optional email
  rpc Login(LoginRequest) returns (AuthResponse);                  // {phone, password, code} — one call, OTP is the 2nd factor
  rpc RefreshToken(RefreshTokenRequest) returns (AuthResponse);    // {refresh_token, sub}
  rpc Logout(LogoutRequest) returns (google.protobuf.Empty);       // caller from metadata; Cognito global sign-out
  rpc ChangePassword(ChangePasswordRequest) returns (google.protobuf.Empty); // {phone, code, new_password} — OTP-based reset
}

message AuthResponse {   // tokens only — profile via UserService.Me
  string access_token = 1;
  string refresh_token = 2;
  int64 access_expires_in = 3;   // seconds
}
```

Removed in the Cognito pivot: `VerifyLogin` (login is one call),
`ResetPassword` (merged into `ChangePassword`), `ValidateToken` (gateway
validates locally via JWKS), and `AuthResponse.account`.

### user_service.proto — self-service (caller from metadata)

```proto
service UserService {
  rpc Me(MeRequest) returns (Account);
  rpc UpdateAccount(UpdateAccountRequest) returns (Account);   // all optional: first/last name, email, language
}
```

### admin_service.proto — OPS/ADMIN tier (spec 3.1, 3.10)

```proto
service AdminService {
  rpc GetAccount(GetAccountRequest) returns (Account);
  rpc ListAccounts(ListAccountsRequest) returns (ListAccountsResponse);        // optional role, status, phone_number
  rpc UpdateAccount(AdminServiceUpdateAccountRequest) returns (Account);              // id + all-optional, incl. role & status
  rpc DeleteAccount(DeleteAccountRequest) returns (google.protobuf.Empty);
}
```

Notes:
- Message names are unique across the package: the self-service update uses
  `UpdateAccountRequest` (no id — caller from metadata), the admin one
  `AdminServiceUpdateAccountRequest` (id + moderation fields).
- Deliberate deviations from the workspace CRUD template, decided 2026-07-23:
  `Me` instead of `GetMe`; admin RPCs split into `AdminService`; **no
  `CreateAccount`** — creation only happens through `Register` for v1; OPS
  accounts are provisioned by the service's `seed` command.
- `AdminService.UpdateAccount` triggers a Cognito global sign-out whenever it
  sets `status` to `suspended`/`banned` (new tokens are already blocked by the
  status check at login/refresh).

### Gateway REST mapping

| Tier | Route | RPC |
|---|---|---|
| public | `POST /v1/auth/otp` | `Auth.SendOtp` |
| public | `POST /v1/auth/register` | `Auth.Register` |
| public | `POST /v1/auth/login` | `Auth.Login` (password + OTP code, one call) |
| public | `POST /v1/auth/refresh` | `Auth.RefreshToken` |
| public | `POST /v1/auth/password` | `Auth.ChangePassword` (OTP-based reset) |
| auth | `POST /v1/auth/logout` | `Auth.Logout` |
| auth | `GET /v1/me` · `PATCH /v1/me` | `User.Me` / `User.UpdateAccount` |
| OPS | `GET /v1/accounts` (+filters) · `GET /v1/accounts/{id}` | `Admin.ListAccounts` / `GetAccount` |
| OPS | `PATCH /v1/accounts/{id}` · `DELETE /v1/accounts/{id}` | `Admin.UpdateAccount` / `DeleteAccount` |

(Token validation is not an endpoint — the gateway middleware verifies JWTs
locally against the Cognito JWKS.)

---

## 4. Service layout & infra changes

- Scaffold `app/services/user` with the **add-service** skill (Cobra `run
  --migrate` / `migrate` / `seed` / `reset`, Makefile, compose, k8s).
- Domains: `internal/business/account` (accounts) and
  `internal/business/auth` (otps, refresh_tokens, passwords, token issuing;
  its usecase composes the account repo). `internal/api/auth`,
  `internal/api/account`, `internal/api/admin` with mappers. SMS interface +
  mock in `internal/integration/sms`.
- **Compose / init**: add `CREATE DATABASE users;` to
  `infra/compose/postgres/init/01-create-databases.sql` (existing volumes need
  a reset or manual `createdb`). New `app/services/user/.env` (DB_*, GRPC_PORT,
  Cognito client id/secret/pool/region, OTP hash pepper + tunables).
- **Gateway**: auth middleware (local JWKS validation via `core/aws` +
  `core/passport`), route tiers (public / authenticated / OPS), gRPC client to
  the user service, REST routes above.
- **Seed**: one OPS account row (phone from `SEED_OPS_PHONE`); its Cognito
  user is provisioned separately (credentials never live in our DB).

## Open questions

1. ~~**Phone normalization**~~ — resolved 2026-07-24: the auth usecase
   normalizes to E.164 with `nyaruka/phonenumbers` (region CD, mobile or
   fixed-line-or-mobile only) before any DB or Cognito call.
2. **Spec amendment** — `01-project-spec.md` §Account model still says
   "passwordless via OTP"; update it to "password + SMS-OTP second factor"
   when this doc is approved.
3. **Notification preferences** — infra doc puts them in this service; not in
   this slice. Additive migration when Notification service lands.

## Progress

- [x] Slice 1 — scaffold service + DB (migrations, views, procedures, queries, sqlc)
      — 2026-07-23. Smoke-tested against a live Postgres: migrate / re-migrate /
      reset / seed, `consume_otp` race guard, `updated_at` triggers, view
      column exclusions all verified. `seed` provisions the OPS account
      (`SEED_OPS_PHONE` / `SEED_OPS_PASSWORD`).
- [x] Slice 2 — protos (`user/v1`: shared, auth, user, admin) + buf generate
      — 2026-07-23. buf lint required `AdminServiceUpdateAccountRequest`
      (standard-name rule) and a repo-wide except for
      `RPC_REQUEST_RESPONSE_UNIQUE` (entity returns + Empty are workspace policy).
- [x] Cognito pivot — 2026-07-24. `core/aws/cognito.go` (sign-up, login,
      refresh, reset, global sign-out, AdminDelete, JWKS ValidateToken),
      `core/passport`, DB stripped of credentials (`cognito_sub` added,
      `refresh_tokens` dropped), protos cleaned (one-call Login, no
      VerifyLogin/ResetPassword/ValidateToken), this doc updated.
- [x] Slice 3 — auth domain — 2026-07-24. Usecase (SendOTP + resend, Register
      with AdminDelete compensation, one-call Login with status check +
      last_login stamp, Refresh with status check, ChangePassword, Logout by
      sub, CheckAndConsumeOTP helper), repo + integration tests, and the
      gRPC surface: `internal/api/auth` handlers + mapper, `internal/sdk/
      grpcerr` (business error → status code), `internal/sdk/identity`
      (caller sub from `x-account-sub` metadata), wired in `app.go`
      (Cognito + sms mock + usecases + Register*Server). Remaining: real
      `crypto/rand` code (111111 hardcoded), Redis rate limits.
- [x] Slice 4 — account domain — 2026-07-24. `UpdateAccount`/`AccountFilter`
      types, repo Update/List/Delete (+ tests), `usecase.Account` (Me,
      UpdateOwnAccount, Get/List, AdminUpdateAccount with global sign-out on
      suspend/ban, DeleteAccount = Cognito AdminDelete then soft delete),
      `internal/api/account` hosting both UserService and AdminService
      handlers over one mapper.
- [x] Slice 5 — gateway auth middleware (local JWKS validation) — 2026-07-24.
      REST routes still pending (next slice: dial `user/client`, mount
      public/auth/OPS route tiers, forward `x-account-sub` from the passport).
- [x] Slice 6 — done 2026-07-25 under
      [user-service-completion](../20260724_1631_user-service-completion/plan.md)
      Slice A: gateway user routes + identity forwarding, smoked live
      through the gateway with real Cognito.

Slices 3–6 are implemented by hand (Mahamadou); the scaffold's
`internal/app.go` marks the registration point with a TODO.
