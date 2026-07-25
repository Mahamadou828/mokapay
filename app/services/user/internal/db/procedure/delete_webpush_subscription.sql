-- Soft delete, ownership-guarded; unknown endpoint or foreign owner is a
-- no-op. Covers both the explicit unsubscribe and the usecase pruning a
-- subscription the push service reported gone (404/410).
CREATE OR REPLACE FUNCTION users_ext.delete_webpush_subscription(
    p_account  UUID,
    p_endpoint TEXT
) RETURNS VOID
LANGUAGE sql AS $$
    UPDATE webpush_subscriptions SET deleted_at = now()
    WHERE account_fk = p_account
      AND endpoint = p_endpoint
      AND deleted_at IS NULL;
$$;
