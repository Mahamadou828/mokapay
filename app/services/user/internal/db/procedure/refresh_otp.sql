-- Resend path: swap in the newly issued code hash and extend the expiry.
-- The resend is counted against attempts so the same 5-attempt cap limits
-- resend spam and failed verifies alike. SETOF: zero rows means the code was
-- consumed or deleted meanwhile — the caller must re-issue, not retry.
CREATE OR REPLACE FUNCTION users_ext.refresh_otp(
    p_id         UUID,
    p_code_hash  TEXT,
    p_expires_at TIMESTAMPTZ
) RETURNS SETOF otps
LANGUAGE sql AS $$
    UPDATE otps SET
        code_hash  = p_code_hash,
        expires_at = p_expires_at,
        attempts   = attempts + 1
    WHERE id = p_id
      AND consumed_at IS NULL
      AND deleted_at IS NULL
    RETURNING *;
$$;
