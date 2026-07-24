-- +migrate Up
CREATE TYPE file_purpose AS ENUM ('sender_proof', 'agent_proof', 'recipient_proof', 'deposit_proof', 'kyc', 'other');
CREATE TYPE file_status  AS ENUM ('pending', 'stored', 'rejected');

CREATE TABLE files (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_nk     UUID NOT NULL,          -- uploading account (user service)
    purpose      file_purpose NOT NULL,
    related_type TEXT,                   -- 'transfer' | 'ledger_request' | ...
    related_nk   UUID,                   -- loose NK: may precede its consumer
    bucket       TEXT NOT NULL,
    storage_key  TEXT NOT NULL,          -- <purpose>/<uuid>, never client-named
    file_name    TEXT NOT NULL,          -- original name, display only
    content_type TEXT NOT NULL,
    size_bytes   BIGINT NOT NULL,
    sha256       TEXT,                   -- set at confirm
    etag         TEXT,                   -- set at confirm
    status       file_status NOT NULL DEFAULT 'pending',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),

    deleted_at   TIMESTAMPTZ
);

CREATE UNIQUE INDEX files_storage_key_uq ON files (storage_key) WHERE deleted_at IS NULL;

-- The consumer lookup: newest file per related entity (+ purpose filter).
CREATE INDEX files_related_idx
    ON files (related_type, related_nk, purpose, created_at DESC)
    WHERE deleted_at IS NULL;

-- The orphan sweep: pending uploads past the presign TTL.
CREATE INDEX files_pending_idx
    ON files (created_at)
    WHERE status = 'pending' AND deleted_at IS NULL;

CREATE TRIGGER files_set_updated_at
    BEFORE UPDATE ON files
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +migrate Down
DROP TABLE files;
DROP TYPE file_status;
DROP TYPE file_purpose;
