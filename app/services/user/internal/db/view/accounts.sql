-- Live accounts. Credentials live in Cognito — this table never stores them.
CREATE OR REPLACE VIEW users_ext.accounts AS
SELECT id,
       phone_number,
       cognito_sub,
       first_name,
       last_name,
       email,
       preferred_language,
       preferred_otp_method,
       role,
       status,
       last_login_at,
       created_at,
       updated_at
FROM accounts
WHERE deleted_at IS NULL;
