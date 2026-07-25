-- +migrate Up
CREATE TYPE agent_profile_status AS ENUM ('pending', 'active', 'suspended');
CREATE TYPE agent_level          AS ENUM ('n1', 'n2', 'n3', 'n4');

CREATE TABLE agent_profiles (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_nk      UUID NOT NULL,     -- user-service account, no cross-service FK
    status          agent_profile_status NOT NULL DEFAULT 'pending',
    level           agent_level NOT NULL DEFAULT 'n1',
    -- The denomination of balance/held/guarantee_floor. Explicitness only:
    -- USD<->CDF sending directions are a transfer/payment-method concern
    -- and never touch the ledger.
    currency        TEXT NOT NULL DEFAULT 'USD',
    balance         BIGINT NOT NULL DEFAULT 0,     -- minor units
    held            BIGINT NOT NULL DEFAULT 0,     -- reserved by open outflow requests (+ transfer holds later)
    guarantee_floor BIGINT NOT NULL DEFAULT 2000,  -- 20 USD in minor units
    city            TEXT,                          -- geo matching input, nullable v1
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at      TIMESTAMPTZ,

    -- The money invariants: any statement that would break them aborts
    -- atomically, whatever bug produced it.
    CONSTRAINT agent_profiles_balance_ck CHECK (balance >= 0),
    CONSTRAINT agent_profiles_held_ck    CHECK (held >= 0 AND held <= balance)
);

-- One profile per account among live rows.
CREATE UNIQUE INDEX agent_profiles_account_uq
    ON agent_profiles (account_nk) WHERE deleted_at IS NULL;

CREATE TRIGGER agent_profiles_set_updated_at
    BEFORE UPDATE ON agent_profiles
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TYPE ledger_request_type AS ENUM
    ('funding', 'withdrawal', 'refund', 'deduction', 'commission');
CREATE TYPE ledger_request_status AS ENUM
    ('initiated', 'pending_agent_action', 'pending_admin_approval',
     'done', 'rejected', 'disputed');  -- disputed reserved for the transfers volley
-- Does the request increase or decrease the balance. Deliberately NOT
-- credit/debit: double-entry vocabulary flips meaning with the point of
-- view; inflow/outflow answers the only question this column has.
CREATE TYPE ledger_direction AS ENUM ('inflow', 'outflow');
-- What a referenced request settles (guarantee -> enrollment, capture ->
-- transfer, later).
CREATE TYPE ledger_reference_type AS ENUM ('enrollment', 'transfer');

-- The mutable workflow object; the ONLY path to a balance change.
CREATE TABLE ledger_requests (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_profile_fk UUID NOT NULL REFERENCES agent_profiles (id),
    type             ledger_request_type NOT NULL,
    direction        ledger_direction NOT NULL,
    amount           BIGINT NOT NULL,             -- always positive; sign lives in direction
    status           ledger_request_status NOT NULL DEFAULT 'initiated',
    -- The entity this request settles. Both NULL for free-standing
    -- requests (agent deposits, withdrawals). NK-style: no DB FK.
    reference_entity_type ledger_reference_type,
    reference_entity_id   UUID,
    proof_file_nk    UUID,          -- attached by UpdateStatus (file service)
    reason           TEXT,          -- human context: deduction cause, reject reason
    created_by       UUID NOT NULL, -- account_nk of the originator (candidate/agent/admin)
    decided_by       UUID,          -- account_nk of the approving/rejecting admin
    decided_at       TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- no deleted_at: a workflow object is history, never hidden

    CONSTRAINT ledger_requests_amount_ck CHECK (amount > 0),
    -- The direction is data, but never in conflict with the type; only
    -- refund is genuinely bidirectional.
    CONSTRAINT ledger_requests_direction_ck CHECK (
        (type IN ('funding', 'commission') AND direction = 'inflow')
        OR (type IN ('withdrawal', 'deduction') AND direction = 'outflow')
        OR type = 'refund'
    ),
    -- The reference pair moves together.
    CONSTRAINT ledger_requests_reference_ck CHECK (
        (reference_entity_type IS NULL) = (reference_entity_id IS NULL)
    )
);

-- One live-or-settled request per referenced entity; a rejected one
-- frees the slot (a rejected guarantee deposit must be retryable).
CREATE UNIQUE INDEX ledger_requests_reference_uq
    ON ledger_requests (reference_entity_type, reference_entity_id)
    WHERE reference_entity_id IS NOT NULL AND status <> 'rejected';
-- The admin worklist ("what awaits approval?").
CREATE INDEX ledger_requests_worklist_idx
    ON ledger_requests (status, created_at DESC);
-- A profile's request history.
CREATE INDEX ledger_requests_profile_idx
    ON ledger_requests (agent_profile_fk, created_at DESC);

CREATE TRIGGER ledger_requests_set_updated_at
    BEFORE UPDATE ON ledger_requests
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Append-only settlement lines: one per settled request, never updated,
-- never deleted. No updated_at/deleted_at on purpose.
CREATE TABLE ledger_lines (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_profile_fk  UUID NOT NULL REFERENCES agent_profiles (id),
    -- UNIQUE: settlement is self-idempotent — even a bug cannot write two
    -- lines for one request.
    ledger_request_fk UUID NOT NULL UNIQUE REFERENCES ledger_requests (id),
    amount            BIGINT NOT NULL,   -- SIGNED: inflow +, outflow −
    balance_after     BIGINT NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The agent statement, newest first.
CREATE INDEX ledger_lines_statement_idx
    ON ledger_lines (agent_profile_fk, created_at DESC);

-- +migrate Down
DROP TABLE ledger_lines;
DROP TABLE ledger_requests;
DROP TABLE agent_profiles;
DROP TYPE ledger_reference_type;
DROP TYPE ledger_direction;
DROP TYPE ledger_request_status;
DROP TYPE ledger_request_type;
DROP TYPE agent_level;
DROP TYPE agent_profile_status;
