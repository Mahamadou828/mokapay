status: approved

# Notifications — web push + in-app inbox (user service)

The notification domain lives **inside the user service** (it owns accounts,
and v1 volume doesn't justify a service). v1 channel is **Web Push**
(`SherClockHolmes/webpush-go`, VAPID) — the pattern mirrors Mahamadou's
prior implementation in `tmp/notification/` (kept as reference until this
lands, then deleted). The notification **row is the source of truth**
(in-app inbox with read state); the push is best-effort delivery on top.

Companions: [plan.md](plan.md) §5 (notification preference booleans — the
`notify_push` flag will gate the push channel once it exists) and the
platform-infra doc (River — see Delivery semantics).

Ownership proposals: **[C]** = Claude, **[M]** = Mahamadou.

## 1. Schema — migration `0002_add_notifications.sql` **[C]**

Two tables, standard audit columns + soft delete:

```sql
CREATE TYPE notification_status AS ENUM ('unread', 'read');

CREATE TABLE notifications (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_fk  UUID NOT NULL REFERENCES accounts (id),
    -- Open cross-service vocabulary (transfer.offered, file.rejected, …),
    -- producers own their names — deliberately TEXT, not an enum.
    event_type  TEXT NOT NULL,
    title       TEXT NOT NULL,
    body        TEXT NOT NULL,
    data        JSONB,               -- deep-link / context payload, opaque here
    status      notification_status NOT NULL DEFAULT 'unread',
    read_at     TIMESTAMPTZ,
    created_at / updated_at / deleted_at
);
CREATE INDEX notifications_unread_ix
    ON notifications (account_fk, created_at DESC)
    WHERE status = 'unread' AND deleted_at IS NULL;   -- badge + inbox query

CREATE TABLE webpush_subscriptions (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_fk  UUID NOT NULL REFERENCES accounts (id),
    endpoint    TEXT NOT NULL,       -- browser push-service URL
    p256dh      TEXT NOT NULL,       -- client public key
    auth        TEXT NOT NULL,       -- client auth secret
    user_agent  TEXT,                -- device hint for a future "manage devices"
    created_at / updated_at / deleted_at
);
CREATE UNIQUE INDEX webpush_subscriptions_endpoint_uq
    ON webpush_subscriptions (endpoint) WHERE deleted_at IS NULL;
```

One account ↔ many subscriptions (each browser/device is one). Re-subscribing
an existing endpoint **upserts** (keys can rotate on the same endpoint).

**Views** (`users_ext`): `notifications` (live rows, newest first),
`webpush_subscriptions` (live rows per account).
**Procedures**: `add_notification`, `mark_notification_read(p_id, p_account)`
(ownership-guarded — the WHERE includes `account_fk = p_account`, never trust
the id alone), `mark_all_notifications_read(p_account)`,
`add_webpush_subscription` (upsert on endpoint), `delete_webpush_subscription
(p_account, p_endpoint)` (soft delete).

## 2. Proto — `NotificationService` in `proto/user/v1` **[C]**

```proto
service NotificationService {
  // Internal: called by other services (system passport) to notify a user.
  // Never exposed as a REST route (same policy as GetAccountBySub).
  rpc PushNotification(PushNotificationRequest) returns (Notification);

  // Self-service: caller identity from the passport.
  rpc ListNotifications(ListNotificationsRequest) returns (ListNotificationsResponse);
  rpc MarkNotificationRead(MarkNotificationReadRequest) returns (Notification);
  rpc MarkAllNotificationsRead(MarkAllNotificationsReadRequest) returns (google.protobuf.Empty);
  rpc SubscribePush(SubscribePushRequest) returns (google.protobuf.Empty);
  rpc UnsubscribePush(UnsubscribePushRequest) returns (google.protobuf.Empty);

  // Public: the client needs it to call PushManager.subscribe().
  rpc GetVapidPublicKey(GetVapidPublicKeyRequest) returns (GetVapidPublicKeyResponse);
}

message PushNotificationRequest {
  string account_id = 1;          // the recipient — the one thing the caller must know
  string event_type = 2;
  string title      = 3;
  string body       = 4;
  optional string data = 5;       // JSON string, stored as JSONB, passed to the client
}
```

`ListNotificationsRequest` carries `optional NotificationStatus status`
(single optional-filter query per the SQL rules) and a `limit` (inbox pull,
newest first — real paging when the mobile UI needs it).

## 3. Business layer — `internal/business/notification/` **[C plumbing, M welcome on usecase]**

Standard stack next to `auth`: types at the root (`Notification`,
`NewNotification`, `PushSubscription`, `NewPushSubscription`, `Status`),
`usecase/`, `repo/` (sqlc pgx, same pattern as the migrated repos).

**Push sender is an interface** on the usecase (like `OTPSender`/`sms.Log`):

```go
type PushSender interface {
    Send(ctx context.Context, sub notification.PushSubscription, payload []byte) error
}
```

- `internal/integration/webpush.Sender` — real implementation over
  webpush-go (VAPID keys + TTL from config). Returns a typed
  `ErrSubscriptionGone` on HTTP 404/410 from the push service.
- `internal/integration/webpush.Log` — dev stand-in when VAPID env is empty
  (same convention as the SMS mock), so local runs need no keys.

**`Push` usecase flow** (the PushNotification RPC):
1. Insert the notification row — this alone decides RPC success.
2. List the account's live subscriptions; for each, `Send` the JSON payload
   `{title, body, event_type, data}`.
3. `ErrSubscriptionGone` → soft-delete that subscription (self-pruning);
   any other send error → log and continue. **Push failures never fail the
   RPC** — the inbox row already exists; zero subscriptions is fine.

Handlers (`internal/api/notification/`): self-service RPCs read the caller
from `passport.FromContext`; `PushNotification` requires
`pass.IsSystem()` (ops-triggered announcements can come later through a
dedicated admin RPC if wanted); `GetVapidPublicKey` is anonymous.

## 4. Gateway routes **[C]**

| Tier | Route | RPC |
|---|---|---|
| public | `GET /v1/push/vapid-key` | `GetVapidPublicKey` |
| authed | `GET /v1/notifications?status=` | `ListNotifications` |
| authed | `POST /v1/notifications/{id}/read` | `MarkNotificationRead` |
| authed | `POST /v1/notifications/read-all` | `MarkAllNotificationsRead` |
| authed | `POST /v1/push/subscriptions` | `SubscribePush` |
| authed | `DELETE /v1/push/subscriptions` | `UnsubscribePush` (endpoint in body) |

`core/client`'s `User` client gains the `NotificationServiceClient`; openapi
+ service.request.http extended.

## 5. Config & keys **[M — keys, C — plumbing]**

`VAPID_PUBLIC_KEY` / `VAPID_PRIVATE_KEY` (+ `PUSH_TTL`, default 30s) in the
user service `.env` — private key is a secret, `.env.example` gets empty
placeholders. Generate once with webpush-go:
`webpush.GenerateVAPIDKeys()` (tiny `cmd` one-off or `npx web-push
generate-vapid-keys`). Empty keys ⇒ the Log sender (dev mode).

## 6. Callers & delivery semantics

- **First consumer**: the file service `UploadWorker` — its purpose-switch
  calls `moka.User.Notification.PushNotification` under the system passport
  it already builds. **[M — it's your worker]**
- Payment service events (transfer offered/completed/disputed) follow the
  same path later.
- **v1 sends synchronously inside the RPC** (row first, best-effort push
  after). If push latency/retries ever matter, the upgrade is a River job
  per send — the callers don't change, only `Push`'s internals. **[M if/when]**
- Once plan.md §5 lands, `Push` checks `notify_push` before sending (the
  row is still written — prefs mute the channel, not the inbox).

## 7. Open decisions

| # | Question | Proposal |
|---|---|---|
| N1 | `data` payload shape | JSON string in proto / JSONB in DB — opaque to the user service, typed by producer+frontend |
| N2 | Ops broadcast ("announce to all agents") | Out of v1; would be an AdminService RPC fanning out through the same usecase |
| N3 | Unread count endpoint | Not yet — `ListNotifications(status=unread, limit=…)` covers the badge for v1 |
| N4 | Retention | Nothing auto-deletes in v1; revisit with a River cron if the table grows |

## Slices & Progress

- [x] Slice 1 — migration 0002 + views/procedures + sqlc + repo (+
      integration test) **[C]** — done 2026-07-25. Both lifecycles tested
      live (inbox add/list/filter, idempotent mark-read with ownership
      guard, mark-all count; subscription upsert/rebind, guarded delete,
      fresh-row-after-soft-delete). Note: JSONB canonicalizes the `data`
      payload formatting — consumers must not expect byte-identical JSON.
- [x] Slice 2 — proto `NotificationService` + usecase (+ webpush/Log
      senders) + handlers, wired in app.go **[C]** — done 2026-07-25.
      Config: `VAPID_PUBLIC_KEY/VAPID_PRIVATE_KEY/VAPID_SUBSCRIBER/PUSH_TTL`
      (empty keys ⇒ Log sender). Usecase integration test proves the
      delivery semantics live (row written, healthy route delivered, gone
      route pruned, failing route tolerated + retried next push). Runtime
      smoke via grpcurl: all 7 RPCs registered; PushNotification without
      system passport → PermissionDenied; ListNotifications anonymous →
      Unauthenticated; GetVapidPublicKey public.
- [x] Slice 3 — gateway routes + MokaClient + openapi/request collection
      **[C]** — done 2026-07-25. Six routes live (vapid-key public, five
      authed); the User-Agent header fills the subscription's device hint
      (never the body). Smoked through a live gateway: vapid-key 200,
      authed routes 403 without a token.
- [ ] Slice 4 — VAPID keys in envs **[M]**; file worker calls
      PushNotification **[M]**
- [x] Cleanup — done 2026-07-25: `tmp/notification/` deleted; a stub
      `tmp/go.mod` fences the remaining scratch files (`tmp/user/`, kept —
      not this plan's to delete) out of the module, so echo is out of
      go.mod and webpush-go stays as a real dependency.
