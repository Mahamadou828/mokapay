-- Failed verify: count the attempt. The usecase rejects when attempts has
-- reached the max BEFORE comparing the code.
CREATE OR REPLACE FUNCTION users_ext.bump_otp_attempts(
    p_id UUID
) RETURNS SETOF otps
LANGUAGE sql AS $$
    UPDATE otps SET attempts = attempts + 1
    WHERE id = p_id AND deleted_at IS NULL
    RETURNING *;
$$;
