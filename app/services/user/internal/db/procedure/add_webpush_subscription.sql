-- Upsert on the live-endpoint unique index: re-subscribing refreshes the
-- keys and re-binds the device to the calling account (last writer wins on
-- a shared browser). A soft-deleted row never conflicts — a fresh row is
-- inserted instead.
CREATE OR REPLACE FUNCTION users_ext.add_webpush_subscription(
    p_account    UUID,
    p_endpoint   TEXT,
    p_p256dh     TEXT,
    p_auth       TEXT,
    p_user_agent TEXT DEFAULT NULL
) RETURNS webpush_subscriptions
LANGUAGE sql AS $$
    INSERT INTO webpush_subscriptions (account_fk, endpoint, p256dh, auth, user_agent)
    VALUES (p_account, p_endpoint, p_p256dh, p_auth, p_user_agent)
    ON CONFLICT (endpoint) WHERE deleted_at IS NULL
    DO UPDATE SET
        account_fk = EXCLUDED.account_fk,
        p256dh     = EXCLUDED.p256dh,
        auth       = EXCLUDED.auth,
        user_agent = COALESCE(EXCLUDED.user_agent, webpush_subscriptions.user_agent)
    RETURNING *;
$$;
