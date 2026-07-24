-- name: GetValidOtp :one
-- Newest live (unconsumed, unexpired) code for the phone/purpose pair.
SELECT id, phone_number, code_hash, purpose, attempts, expires_at, created_at, updated_at
FROM users_ext.otps
WHERE phone_number = sqlc.arg(phone_number)
  AND purpose = sqlc.arg(purpose)::otp_purpose
ORDER BY created_at DESC
LIMIT 1;

-- name: AddOtp :one
SELECT * FROM users_ext.add_otp(
    sqlc.arg(phone_number),
    sqlc.arg(code_hash),
    sqlc.arg(purpose)::otp_purpose,
    sqlc.arg(expires_at)
);

-- name: RefreshOtp :one
-- Resend: new code hash + expiry, counts the resend against attempts.
-- Zero rows (sql.ErrNoRows) = consumed or deleted meanwhile.
SELECT * FROM users_ext.refresh_otp(
    sqlc.arg(id)::uuid,
    sqlc.arg(code_hash),
    sqlc.arg(expires_at)
);

-- name: ConsumeOtp :one
-- Zero rows (sql.ErrNoRows) = already consumed, expired, or lost the race.
SELECT * FROM users_ext.consume_otp(sqlc.arg(id)::uuid);

-- name: BumpOtpAttempts :one
SELECT * FROM users_ext.bump_otp_attempts(sqlc.arg(id)::uuid);
