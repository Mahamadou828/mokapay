-- Returns how many notifications were flipped (0 is a fine answer).
CREATE OR REPLACE FUNCTION users_ext.mark_all_notifications_read(
    p_account UUID
) RETURNS BIGINT
LANGUAGE sql AS $$
    WITH flipped AS (
        UPDATE notifications SET
            status  = 'read',
            read_at = COALESCE(read_at, now())
        WHERE account_fk = p_account
          AND status = 'unread'
          AND deleted_at IS NULL
        RETURNING 1
    )
    SELECT count(*) FROM flipped;
$$;
