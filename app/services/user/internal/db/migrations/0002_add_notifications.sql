-- +migrate Up
CREATE TYPE notification_status AS ENUM ('unread', 'read');

CREATE TABLE notifications (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_fk  UUID NOT NULL REFERENCES accounts (id),
    -- Open cross-service vocabulary (transfer.offered, file.rejected, …);
    -- producers own their names — deliberately TEXT, not an enum.
    event_type  TEXT NOT NULL,
    title       TEXT NOT NULL,
    body        TEXT NOT NULL,
    data        JSONB,                 -- deep-link / context payload, opaque here
    status      notification_status NOT NULL DEFAULT 'unread',
    read_at     TIMESTAMPTZ,           -- stamped once, on first mark-read
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at  TIMESTAMPTZ
);

-- The inbox pull: an account's live notifications, newest first. Serves the
-- unread badge too (status is a filter on top).
CREATE INDEX notifications_inbox_idx
    ON notifications (account_fk, created_at DESC)
    WHERE deleted_at IS NULL;

CREATE TRIGGER notifications_set_updated_at
    BEFORE UPDATE ON notifications
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- One row per browser/device push registration; the endpoint + key pair is
-- issued by the browser's push service (PushManager.subscribe).
CREATE TABLE webpush_subscriptions (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_fk  UUID NOT NULL REFERENCES accounts (id),
    endpoint    TEXT NOT NULL,         -- push-service URL, unique per subscription
    p256dh      TEXT NOT NULL,         -- client public key
    auth        TEXT NOT NULL,         -- client auth secret
    user_agent  TEXT,                  -- device hint for a future "manage devices"
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at  TIMESTAMPTZ
);

-- Unique among live rows; re-subscribing the same endpoint upserts (keys can
-- rotate, and a device switching accounts re-binds to the new one).
CREATE UNIQUE INDEX webpush_subscriptions_endpoint_uq
    ON webpush_subscriptions (endpoint) WHERE deleted_at IS NULL;

CREATE INDEX webpush_subscriptions_account_idx
    ON webpush_subscriptions (account_fk) WHERE deleted_at IS NULL;

CREATE TRIGGER webpush_subscriptions_set_updated_at
    BEFORE UPDATE ON webpush_subscriptions
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +migrate Down
DROP TABLE webpush_subscriptions;
DROP TABLE notifications;
DROP TYPE notification_status;
