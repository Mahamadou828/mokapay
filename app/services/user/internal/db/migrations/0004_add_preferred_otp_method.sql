-- +migrate Up
-- How an account wants its OTP delivered. Registration is always SMS (no
-- account row exists yet to hold a preference); login/reset honour this.
CREATE TYPE otp_method AS ENUM ('sms', 'email');

-- Defaults to 'sms' so every existing account keeps today's behaviour, and
-- an account with the email preference but no email on file falls back to
-- SMS at send time rather than losing OTP delivery entirely.
ALTER TABLE accounts
    ADD COLUMN preferred_otp_method otp_method NOT NULL DEFAULT 'sms';

-- +migrate Down
ALTER TABLE accounts DROP COLUMN preferred_otp_method;
DROP TYPE otp_method;
