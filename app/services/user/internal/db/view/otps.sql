-- Live (unconsumed, unexpired) codes only; the verify path reads the newest
-- row per (phone_number, purpose).
CREATE OR REPLACE VIEW users_ext.otps AS
SELECT id,
       phone_number,
       code_hash,
       purpose,
       attempts,
       expires_at,
       created_at,
       updated_at
FROM otps
WHERE consumed_at IS NULL
  AND expires_at > now()
  AND deleted_at IS NULL;
