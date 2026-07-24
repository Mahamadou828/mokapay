-- +migrate Up
CREATE TYPE account_role     AS ENUM ('user', 'pending_agent', 'agent', 'ops', 'admin');
CREATE TYPE account_status   AS ENUM ('pending', 'active', 'suspended', 'banned');
CREATE TYPE account_language AS ENUM ('fr', 'en');
CREATE TYPE otp_purpose      AS ENUM ('register', 'login', 'reset');

CREATE TABLE accounts (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    phone_number       TEXT NOT NULL,              -- E.164, normalized at the API edge
    cognito_sub        TEXT,                       -- Cognito user id (sub claim); NULL until provisioned
    first_name         TEXT NOT NULL,
    last_name          TEXT NOT NULL,
    email              TEXT,
    preferred_language account_language NOT NULL DEFAULT 'fr',
    role               account_role     NOT NULL DEFAULT 'user',
    status             account_status   NOT NULL DEFAULT 'active',
    last_login_at      TIMESTAMPTZ,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),

    deleted_at         TIMESTAMPTZ
);

-- Phone is the login identity: unique among live rows only (soft delete frees it).
CREATE UNIQUE INDEX accounts_phone_number_uq
    ON accounts (phone_number) WHERE deleted_at IS NULL;

-- One account per Cognito user, among live provisioned rows only.
CREATE UNIQUE INDEX accounts_cognito_sub_uq
    ON accounts (cognito_sub) WHERE deleted_at IS NULL AND cognito_sub IS NOT NULL;

CREATE TRIGGER accounts_set_updated_at
    BEFORE UPDATE ON accounts
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE otps (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    phone_number TEXT NOT NULL,          -- no FK: a register OTP precedes the account
    code_hash    TEXT NOT NULL,          -- bcrypt of the 6-digit code
    purpose      otp_purpose NOT NULL,
    attempts     INT  NOT NULL DEFAULT 0,
    expires_at   TIMESTAMPTZ NOT NULL,   -- created_at + OTP_TTL
    consumed_at  TIMESTAMPTZ,            -- set once, atomically, on successful verify
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at   TIMESTAMPTZ
);

-- The only OTP lookup: latest live code for (phone, purpose).
CREATE INDEX otps_lookup_idx
    ON otps (phone_number, purpose, created_at DESC)
    WHERE consumed_at IS NULL AND deleted_at IS NULL;

CREATE TRIGGER otps_set_updated_at
    BEFORE UPDATE ON otps
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +migrate Down
DROP TABLE otps;
DROP TABLE accounts;
DROP TYPE otp_purpose;
DROP TYPE account_language;
DROP TYPE account_status;
DROP TYPE account_role;
