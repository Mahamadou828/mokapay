-- Atomic consume: two racing verifies can both pass the bcrypt check in Go,
-- but only one wins this UPDATE. SETOF so the loser gets zero rows
-- (sql.ErrNoRows through sqlc) instead of a NULL-filled record.
CREATE OR REPLACE FUNCTION users_ext.consume_otp(
    p_id UUID
) RETURNS SETOF otps
LANGUAGE sql AS $$
    UPDATE otps SET consumed_at = now()
    WHERE id = p_id
      AND consumed_at IS NULL
      AND expires_at > now()
      AND deleted_at IS NULL
    RETURNING *;
$$;
