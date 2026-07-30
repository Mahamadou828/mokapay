-- +migrate Up
-- Who a rule was evaluated against. Recipients are off-platform (cash
-- payout) — they are identified by phone, never by an account.
CREATE TYPE compliance_subject_type AS ENUM ('sender', 'agent', 'recipient');

-- The fixed AML rule set. Adding a rule is an ALTER TYPE … ADD VALUE in a
-- new migration — deliberately a schema decision, not a config toggle.
CREATE TYPE compliance_rule_code AS ENUM (
    'daily_volume_limit', 'weekly_structuring', 'sender_volume_spike',
    'agent_volume_spike', 'beneficiary_concentration', 'hourly_velocity',
    'night_time_structuring', 'new_account_high_volume'
);

CREATE TYPE compliance_severity AS ENUM ('low', 'medium', 'high');

CREATE TYPE compliance_alert_status AS ENUM
    ('open', 'under_review', 'escalated', 'resolved', 'dismissed');

-- closed_sar records that a Suspicious Activity Report was filed with the
-- regulator — a distinct regulatory outcome, not a synonym of "closed".
CREATE TYPE compliance_case_status AS ENUM
    ('open', 'investigating', 'closed_sar', 'closed_no_action');

-- The audit log: one row per rule per evaluation, triggered or not.
-- Append-only — no updated_at, no deleted_at (same exception the payment
-- service uses for ledger_lines/transfer_events): "we ran this rule
-- against these inputs at this time" is a fact, and a fact is never
-- edited.
CREATE TABLE rule_evaluations (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    transfer_nk     UUID NOT NULL,            -- payment.transfers, no cross-service FK
    rule_code       compliance_rule_code NOT NULL,
    subject_type    compliance_subject_type NOT NULL,
    account_nk      UUID,                     -- user.accounts; sender/agent subjects
    recipient_phone TEXT,                     -- recipient subjects
    triggered       BOOLEAN NOT NULL,
    severity        compliance_severity,      -- NULL when not triggered
    -- Snapshotted at evaluation time: retuning the severity→points table
    -- never rewrites the history of scores already assigned.
    risk_points     INT NOT NULL DEFAULT 0,
    -- The metrics snapshot the caller supplied — the audit answer to
    -- "what was actually checked?".
    inputs          JSONB NOT NULL,
    evaluated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT rule_evaluations_risk_points_ck CHECK (risk_points >= 0),
    -- A triggered row always carries a severity; a quiet one never does.
    CONSTRAINT rule_evaluations_severity_ck CHECK (
        (triggered AND severity IS NOT NULL) OR (NOT triggered AND severity IS NULL)
    ),
    -- The subject and its identifier move together.
    CONSTRAINT rule_evaluations_subject_ck CHECK (
        (subject_type IN ('sender', 'agent') AND account_nk IS NOT NULL AND recipient_phone IS NULL)
     OR (subject_type = 'recipient' AND recipient_phone IS NOT NULL AND account_nk IS NULL)
    )
);

-- The risk-score scan (SUM over a rolling window per account).
CREATE INDEX rule_evaluations_account_ix
    ON rule_evaluations (account_nk, evaluated_at DESC) WHERE account_nk IS NOT NULL;
-- "what did we check on this transfer?"
CREATE INDEX rule_evaluations_transfer_ix ON rule_evaluations (transfer_nk);

-- The staff lifecycle on top of a triggered evaluation. 1:1 with its
-- evaluation, created in the same statement (see add_rule_evaluation).
CREATE TABLE alerts (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- UNIQUE: an evaluation can never sprout a second alert, whatever bug
    -- replays the insert.
    rule_evaluation_fk UUID NOT NULL UNIQUE REFERENCES rule_evaluations (id),
    status             compliance_alert_status NOT NULL DEFAULT 'open',
    assignee_nk        UUID,   -- user.accounts (staff), nullable
    resolution_note    TEXT,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at         TIMESTAMPTZ
);

-- The review worklist ("what is open?").
CREATE INDEX alerts_status_ix
    ON alerts (status, created_at DESC) WHERE deleted_at IS NULL;

CREATE TRIGGER alerts_set_updated_at
    BEFORE UPDATE ON alerts
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE SEQUENCE case_ref_seq;

CREATE TABLE cases (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- Generated server-side, same shape as MKP-/DEP-: CASE-2026-000042.
    reference   TEXT NOT NULL
        DEFAULT 'CASE-' || to_char(now(), 'YYYY') || '-' ||
                lpad(nextval('case_ref_seq')::text, 6, '0'),
    status      compliance_case_status NOT NULL DEFAULT 'open',
    assignee_nk UUID,
    summary     TEXT,
    closed_at   TIMESTAMPTZ,   -- stamped on the move to a closed_* status
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at  TIMESTAMPTZ
);

CREATE UNIQUE INDEX cases_reference_uq ON cases (reference) WHERE deleted_at IS NULL;
CREATE INDEX cases_status_ix ON cases (status, created_at DESC) WHERE deleted_at IS NULL;

CREATE TRIGGER cases_set_updated_at
    BEFORE UPDATE ON cases
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Junction rather than a case_fk on alerts: an alert can be moved between
-- cases (or referenced by a second one) without a migration.
CREATE TABLE case_alerts (
    case_fk    UUID NOT NULL REFERENCES cases (id),
    alert_fk   UUID NOT NULL REFERENCES alerts (id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (case_fk, alert_fk)
);

-- "which cases is this alert part of?"
CREATE INDEX case_alerts_alert_ix ON case_alerts (alert_fk);

-- +migrate Down
DROP TABLE case_alerts;
DROP TABLE cases;
DROP SEQUENCE case_ref_seq;
DROP TABLE alerts;
DROP TABLE rule_evaluations;
DROP TYPE compliance_case_status;
DROP TYPE compliance_alert_status;
DROP TYPE compliance_severity;
DROP TYPE compliance_rule_code;
DROP TYPE compliance_subject_type;
