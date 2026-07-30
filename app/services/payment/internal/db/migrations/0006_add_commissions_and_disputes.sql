-- +migrate Up
-- Commissions (accrual + weekly payout batch) and disputes — the money
-- effects of a transfer's two endings.

-- The lifecycle of one accrual. Never stored on the row: it is DERIVED in
-- the view from batch_fk + the batch's ledger request (a stored copy is
-- exactly the denormalized aggregate that desyncs).
CREATE TYPE commission_status AS ENUM ('accrued', 'batched', 'paid');

-- One weekly payout per agent: the accruals of an accounting week grouped
-- into a single `commission` ledger request. The request is the ONLY thing
-- that moves the balance — this table just names what it pays for.
CREATE TABLE commission_batches (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_profile_fk  UUID NOT NULL REFERENCES agent_profiles (id),
    accounting_week   DATE NOT NULL,   -- the Tuesday that opens the week
    commission_count  INT NOT NULL,
    total_amount      BIGINT NOT NULL, -- USD minor units: sum of payable_amount
    -- UNIQUE: a batch is one request and a request pays one batch.
    ledger_request_fk UUID NOT NULL UNIQUE REFERENCES ledger_requests (id),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- no deleted_at: a payout record is history, never hidden

    CONSTRAINT commission_batches_amounts_ck
        CHECK (commission_count > 0 AND total_amount > 0)
);

-- THE idempotency guard of the weekly run: one batch per agent per week,
-- so a re-run (periodic job restart, manual `settle-commissions`) can
-- never pay the same week twice. No reconciliation script can ever be
-- needed to clean up after it.
CREATE UNIQUE INDEX commission_batches_week_uq
    ON commission_batches (agent_profile_fk, accounting_week);
CREATE INDEX commission_batches_agent_idx
    ON commission_batches (agent_profile_fk, accounting_week DESC);

CREATE TRIGGER commission_batches_set_updated_at
    BEFORE UPDATE ON commission_batches
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- One accrual per completed transfer — the single bookkeeping place.
CREATE TABLE commissions (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- UNIQUE: accrual is self-idempotent. Every path to `done` (client
    -- confirm, auto-confirm timer, dispute resolved in the agent's favour)
    -- accrues, and a replay can never double-book.
    transfer_fk           UUID NOT NULL UNIQUE REFERENCES transfers (id),
    agent_profile_fk      UUID NOT NULL REFERENCES agent_profiles (id),
    -- The fee and its 60/30/10 split (D9: flat for every agent level),
    -- denominated in the TRANSFER's from_currency.
    fee_currency          currency NOT NULL,
    fee_amount            BIGINT NOT NULL,
    agent_amount          BIGINT NOT NULL,
    platform_amount       BIGINT NOT NULL,
    guarantee_fund_amount BIGINT NOT NULL,
    -- The FX margin: 100% platform, on top of the fee split — but in the
    -- to_currency, so it is kept in its own column with its own currency.
    -- Folding it into platform_amount would add francs to cents (the
    -- prototype's mixed-currency accounting bug).
    margin_currency       currency NOT NULL,
    margin_amount         BIGINT NOT NULL DEFAULT 0,
    -- What the weekly batch actually credits: the agent's share expressed
    -- in the guarantee balance's denomination (USD minor units), converted
    -- at the transfer's OWN locked rate (hold_amount : total_amount) —
    -- never a fresh rate lookup, so the payout matches the quote the
    -- client was given.
    payable_amount        BIGINT NOT NULL,
    accounting_week       DATE NOT NULL,  -- the Tuesday that opens the week
    batch_fk              UUID REFERENCES commission_batches (id),
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- no deleted_at: an accrual is immutable bookkeeping

    CONSTRAINT commissions_amounts_ck CHECK (
        fee_amount >= 0 AND agent_amount >= 0 AND platform_amount >= 0
        AND guarantee_fund_amount >= 0 AND margin_amount >= 0
        AND payable_amount >= 0
    ),
    -- The split is exhaustive: no cent of the fee is lost to rounding.
    CONSTRAINT commissions_split_ck CHECK
        (agent_amount + platform_amount + guarantee_fund_amount = fee_amount)
);

-- The batch worklist ("what is still unpaid, per agent per week?").
CREATE INDEX commissions_due_idx
    ON commissions (accounting_week, agent_profile_fk) WHERE batch_fk IS NULL;
-- The agent's commission statement.
CREATE INDEX commissions_agent_idx
    ON commissions (agent_profile_fk, created_at DESC);

CREATE TRIGGER commissions_set_updated_at
    BEFORE UPDATE ON commissions
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ---------------------------------------------------------------------------
-- Disputes

CREATE TYPE dispute_party AS ENUM ('client', 'agent', 'system');
CREATE TYPE dispute_reason AS ENUM
    ('agent_not_paid', 'recipient_not_received', 'client_fraud', 'other');
CREATE TYPE dispute_status AS ENUM ('open', 'resolved');
-- The admin ruling. Each value fixes the whole money outcome (hold,
-- balance, terminal status) — see the resolve_dispute_* procedures.
CREATE TYPE dispute_resolution AS ENUM
    ('agent_right', 'agent_fault', 'client_fraud', 'no_fault');
-- What the resolution did with the frozen collateral.
CREATE TYPE hold_outcome AS ENUM ('released', 'captured');

-- One dispute record per contested transfer. While it is open the
-- transfer sits in `disputed` and the hold STAYS held — the collateral is
-- what funds the fix (spec core rule).
CREATE TABLE disputes (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    transfer_fk     UUID NOT NULL REFERENCES transfers (id),
    opened_by_party dispute_party NOT NULL,
    opened_by       UUID,           -- account NK; NULL = system auto-dispute
    reason          dispute_reason NOT NULL,
    details         TEXT,
    -- Snapshot of the transfer at freeze time: where it was, what is held,
    -- and what the client would be owed back. Stamped once so a later
    -- resolution reads the dispute, not a moved-on transfer.
    from_status     transfer_status NOT NULL,
    hold_amount     BIGINT NOT NULL,  -- USD minor units, frozen
    refund_currency currency NOT NULL,
    refund_amount   BIGINT NOT NULL,  -- the client's total_amount
    status          dispute_status NOT NULL DEFAULT 'open',
    resolution      dispute_resolution,
    hold_outcome    hold_outcome,
    resolution_note TEXT,
    -- The capture, on agent_fault: the deduction request that settled the
    -- guarantee into the platform's hands.
    deduction_fk    UUID REFERENCES ledger_requests (id),
    resolved_by     UUID,             -- account NK of the deciding admin
    resolved_at     TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- no deleted_at: a workflow object is history, never hidden

    CONSTRAINT disputes_amounts_ck CHECK (hold_amount > 0 AND refund_amount > 0),
    -- Resolution fields move together with the status.
    CONSTRAINT disputes_resolution_ck CHECK (
        (status = 'open'
         AND resolution IS NULL AND hold_outcome IS NULL
         AND resolved_at IS NULL AND deduction_fk IS NULL)
        OR (status = 'resolved'
            AND resolution IS NOT NULL AND hold_outcome IS NOT NULL
            AND resolved_at IS NOT NULL)
    ),
    -- A capture always names its deduction; a release never has one.
    CONSTRAINT disputes_capture_ck CHECK (
        (hold_outcome = 'captured') = (deduction_fk IS NOT NULL)
        OR hold_outcome IS NULL
    )
);

-- At most one open dispute per transfer (the status guard in open_dispute
-- already prevents a second one; this is the last-resort guarantee).
CREATE UNIQUE INDEX disputes_open_uq
    ON disputes (transfer_fk) WHERE status = 'open';
-- The ops queue ("what awaits a ruling?").
CREATE INDEX disputes_worklist_idx ON disputes (status, created_at DESC);
CREATE INDEX disputes_transfer_idx ON disputes (transfer_fk, created_at DESC);

CREATE TRIGGER disputes_set_updated_at
    BEFORE UPDATE ON disputes
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +migrate Down
DROP TABLE disputes;
DROP TABLE commissions;
DROP TABLE commission_batches;
DROP TYPE hold_outcome;
DROP TYPE dispute_resolution;
DROP TYPE dispute_status;
DROP TYPE dispute_reason;
DROP TYPE dispute_party;
DROP TYPE commission_status;
