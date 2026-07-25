-- +migrate Up
CREATE TYPE enrollment_status AS ENUM
    ('invited', 'accepted', 'declined', 'approved', 'cancelled');

CREATE TABLE enrollment_requests (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_fk      UUID NOT NULL REFERENCES accounts (id),  -- the candidate
    invited_by      UUID NOT NULL REFERENCES accounts (id),  -- ops/admin caller
    status          enrollment_status NOT NULL DEFAULT 'invited',
    tos_version     TEXT,             -- which terms were accepted
    tos_accepted_at TIMESTAMPTZ,      -- stamped on accept
    decided_by      UUID REFERENCES accounts (id),  -- who closed it (admin, or the candidate on decline)
    decided_at      TIMESTAMPTZ,      -- terminal-transition stamp
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at      TIMESTAMPTZ
);

-- One live enrollment per account; a double invite is a clean 23505.
-- Terminal rows (declined/approved/cancelled) stay as history and never
-- block a re-invite.
CREATE UNIQUE INDEX enrollment_requests_live_uq
    ON enrollment_requests (account_fk)
    WHERE status IN ('invited', 'accepted') AND deleted_at IS NULL;

-- The ops dashboard listing ("who's stuck at accepted?").
CREATE INDEX enrollment_requests_status_idx
    ON enrollment_requests (status, created_at DESC)
    WHERE deleted_at IS NULL;

CREATE TRIGGER enrollment_requests_set_updated_at
    BEFORE UPDATE ON enrollment_requests
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +migrate Down
DROP TABLE enrollment_requests;
DROP TYPE enrollment_status;
