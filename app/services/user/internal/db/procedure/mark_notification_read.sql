-- Ownership-guarded (id alone is never trusted) and idempotent: re-reading
-- keeps the original read_at. SETOF so a wrong id/owner yields zero rows
-- (pgx.ErrNoRows through sqlc) instead of a NULL-filled record.
CREATE OR REPLACE FUNCTION users_ext.mark_notification_read(
    p_id      UUID,
    p_account UUID
) RETURNS SETOF notifications
LANGUAGE sql AS $$
    UPDATE notifications SET
        status  = 'read',
        read_at = COALESCE(read_at, now())
    WHERE id = p_id
      AND account_fk = p_account
      AND deleted_at IS NULL
    RETURNING *;
$$;
