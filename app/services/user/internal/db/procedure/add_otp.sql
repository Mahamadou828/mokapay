CREATE OR REPLACE FUNCTION users_ext.add_otp(
    p_phone_number TEXT,
    p_code_hash    TEXT,
    p_purpose      otp_purpose,
    p_expires_at   TIMESTAMPTZ
) RETURNS otps
LANGUAGE sql AS $$
    INSERT INTO otps (phone_number, code_hash, purpose, expires_at)
    VALUES (p_phone_number, p_code_hash, p_purpose, p_expires_at)
    RETURNING *;
$$;
