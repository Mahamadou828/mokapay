status: approved

# Compliance service — design

A new microservice (`app/services/compliance`, own Postgres DB `compliance`,
scaffolded via the `add-service` skill), following the workspace's standard
layer stack. Owns AML rule evaluation, alerts, and case management. Evaluates
async (River job from `payment`), never blocks a transfer.

## Key architecture decision: where the metrics come from

Several rules need rolling aggregates over `transfers` (sender's 24h/7d/30d
volume, agent's daily/30d-average volume, a recipient's distinct-sender count,
hourly/night-time transfer counts, account age). That data is owned by
`payment` (transfers) and `user` (account creation date) — **compliance does
not duplicate it**. Instead: **`payment` computes the metrics** (it already
has the indexed `transfers` table and is best positioned to aggregate
efficiently) **and passes them as a snapshot on the evaluation request.**
Compliance is a stateless-per-call rule/threshold engine over whatever
snapshot it's given, plus the append-only store of every evaluation it has
ever run. This keeps compliance's DB self-contained (no NK reads back into
payment/user at evaluation time) and makes the audit trail literally record
"ran this rule, against these inputs, at this time" — the strongest form of
provability.

`payment` computing the metrics is real, non-trivial work (new queries against
`transfers`/`ledger_lines`) — it lands as part of the `payment`-side
integration task, not the compliance service build itself.

## Schema (`app/services/compliance/internal/db/`)

### Migration — enums

```sql
CREATE TYPE compliance_subject_type AS ENUM ('sender', 'agent', 'recipient');
CREATE TYPE compliance_rule_code AS ENUM (
    'daily_volume_limit', 'weekly_structuring', 'sender_volume_spike',
    'agent_volume_spike', 'beneficiary_concentration', 'hourly_velocity',
    'night_time_structuring', 'new_account_high_volume'
);
CREATE TYPE compliance_severity AS ENUM ('low', 'medium', 'high');
CREATE TYPE compliance_alert_status AS ENUM
    ('open', 'under_review', 'escalated', 'resolved', 'dismissed');
CREATE TYPE compliance_case_status AS ENUM
    ('open', 'investigating', 'closed_sar', 'closed_no_action');
```

### `rule_evaluations` — immutable audit log (no `updated_at`/`deleted_at`,
same append-only exception already used for `ledger_lines`/`transfer_events`)

```sql
CREATE TABLE rule_evaluations (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    transfer_nk     UUID NOT NULL,           -- payment.transfers, no FK (cross-service)
    rule_code       compliance_rule_code NOT NULL,
    subject_type    compliance_subject_type NOT NULL,
    account_nk      UUID,                    -- user.accounts; set when subject is sender/agent
    recipient_phone TEXT,                    -- set when subject is recipient
    triggered       BOOLEAN NOT NULL,
    severity        compliance_severity,     -- NULL when not triggered
    risk_points     INT NOT NULL DEFAULT 0,  -- snapshot: points assigned at evaluation time
    inputs          JSONB NOT NULL,          -- the metrics snapshot payment sent (audit: what was checked)
    evaluated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT rule_evaluations_subject_ck CHECK (
        (subject_type IN ('sender', 'agent') AND account_nk IS NOT NULL AND recipient_phone IS NULL) OR
        (subject_type = 'recipient' AND recipient_phone IS NOT NULL AND account_nk IS NULL)
    )
);
CREATE INDEX rule_evaluations_account_ix
    ON rule_evaluations (account_nk, evaluated_at DESC) WHERE account_nk IS NOT NULL;
CREATE INDEX rule_evaluations_transfer_ix ON rule_evaluations (transfer_nk);
```

### `alerts` — mutable, staff-driven lifecycle; standard audit columns + soft delete

```sql
CREATE TABLE alerts (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    rule_evaluation_fk UUID NOT NULL REFERENCES rule_evaluations (id),
    status             compliance_alert_status NOT NULL DEFAULT 'open',
    assignee_nk        UUID,                 -- user.accounts (staff), nullable
    resolution_note    TEXT,
    created_at / updated_at / deleted_at
);
CREATE INDEX alerts_status_ix ON alerts (status, created_at DESC) WHERE deleted_at IS NULL;
```
Only a `triggered = true` evaluation ever gets an alert row (1:1, created in the
same procedure call as the evaluation insert when `p_triggered`).

### `cases` + `case_alerts` (junction — an alert could plausibly move between
cases or be referenced once; junction keeps that open without a migration)

```sql
CREATE TABLE cases (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    reference    TEXT NOT NULL,  -- CASE-YYYY-NNNNNN, unique, same pattern as MKP-/DEP-
    status       compliance_case_status NOT NULL DEFAULT 'open',
    assignee_nk  UUID,
    summary      TEXT,
    closed_at    TIMESTAMPTZ,
    created_at / updated_at / deleted_at
);
CREATE UNIQUE INDEX cases_reference_uq ON cases (reference) WHERE deleted_at IS NULL;

CREATE TABLE case_alerts (
    case_fk    UUID NOT NULL REFERENCES cases (id),
    alert_fk   UUID NOT NULL REFERENCES alerts (id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (case_fk, alert_fk)
);
```

### Views (`compliance_ext`)

- `rule_evaluations` — explicit column list, no `deleted_at` (table has none).
- `alerts` — joins `rule_evaluations` for `rule_code`/`account_nk`/`transfer_nk`/
  `subject_type` (per the FK-join column rule: alerts only projects its own FK,
  not a duplicate id from the joined row).
- `cases` — plain live-row view.
- `account_risk` — **the risk score, computed, never stored**:
  ```sql
  CREATE VIEW compliance_ext.account_risk AS
  SELECT account_nk, SUM(risk_points) AS risk_score, MAX(evaluated_at) AS last_evaluated_at
  FROM rule_evaluations
  WHERE account_nk IS NOT NULL AND evaluated_at > now() - INTERVAL '90 days'
  GROUP BY account_nk;
  ```
  The 90-day lookback is a schema-level constant (a migration to change, same
  as any other structural choice); the score→level bucketing (low/medium/
  high/critical at 30/60/80) is applied in the Go usecase from env config
  (`core/config`), not in SQL — mirrors how `transfer`'s fee bps are
  env-config read in the usecase, not hardcoded in a view.
- `dashboard_counts` (or computed directly in the usecase via several
  targeted queries — either is fine): open/high/medium/low alert counts,
  cases open/escalated, flagged volume — feeds `GetComplianceDashboard`.

### Procedures

- `add_rule_evaluation(p_transfer_nk, p_rule_code, p_subject_type,
  p_account_nk, p_recipient_phone, p_triggered, p_severity, p_risk_points,
  p_inputs jsonb) RETURNS rule_evaluations` — the only write path into the
  audit log; also inserts the paired `alerts` row when `p_triggered`
  (single procedure, one transaction — evaluation + alert are one fact).
- `update_alert_status(p_id, p_status, p_assignee_nk DEFAULT NULL,
  p_resolution_note DEFAULT NULL) RETURNS alerts`.
- `add_case(p_reference, p_summary DEFAULT NULL) RETURNS cases`.
- `update_case(p_id, p_status DEFAULT NULL, p_assignee_nk DEFAULT NULL,
  p_summary DEFAULT NULL) RETURNS cases` — sets `closed_at = now()` when
  `p_status` moves to `closed_sar`/`closed_no_action`.
- `add_case_alert(p_case_id, p_alert_id) RETURNS case_alerts` (idempotent:
  `ON CONFLICT DO NOTHING` then re-select, since the PK is the pair).

## Proto (`proto/compliance/v1`)

```proto
service ComplianceService {
  // Internal only (system passport), called by payment via a River job —
  // never exposed on the gateway.
  rpc EvaluateTransfer(EvaluateTransferRequest) returns (EvaluateTransferResponse);
}

message EvaluateTransferRequest {
  string transfer_id = 1;
  TransferMetrics sender = 2;     // subject_type=sender inputs
  TransferMetrics agent = 3;      // subject_type=agent inputs (optional — not every transfer stage has one yet)
  RecipientMetrics recipient = 4; // subject_type=recipient inputs
}

// Whatever payment already computed — see the payment-side integration task.
message TransferMetrics {
  string account_id = 1;
  int64 current_amount_usd = 2;
  int64 rolling_24h_volume_usd = 3;
  int64 rolling_7d_volume_usd = 4;
  int32 rolling_7d_count = 5;
  int64 rolling_30d_avg_daily_volume_usd = 6;
  int32 rolling_1h_count = 7;
  int32 rolling_7d_nighttime_count = 8;   // configured tz, e.g. Africa/Kinshasa
  int32 account_age_days = 9;
}

message RecipientMetrics {
  string phone = 1;
  int32 distinct_senders_7d = 2;
}

service AdminService {
  rpc ListAlerts(ListAlertsRequest) returns (ListAlertsResponse);        // filter: status
  rpc GetAlert(GetAlertRequest) returns (Alert);
  rpc UpdateAlertStatus(UpdateAlertStatusRequest) returns (Alert);
  rpc ListCases(ListCasesRequest) returns (ListCasesResponse);           // filter: status
  rpc CreateCase(CreateCaseRequest) returns (Case);
  rpc UpdateCase(UpdateCaseRequest) returns (Case);
  rpc AddAlertToCase(AddAlertToCaseRequest) returns (google.protobuf.Empty);
  rpc GetAccountRisk(GetAccountRiskRequest) returns (AccountRisk);       // score + level for one account
  rpc GetDashboard(GetDashboardRequest) returns (ComplianceDashboard);   // counts, feeds admin-app page
}
```

## Rule thresholds (env config, `internal/config.go` — ops can tune without a
migration; defaults below are the prototype's values, not yet validated
against real regulatory requirements)

| Rule | Config key | Default |
|---|---|---|
| Daily volume limit | `AML_DAILY_LIMIT_USD` | 10000 |
| Weekly structuring window volume | `AML_WEEKLY_LIMIT_USD` | 10000 |
| Weekly structuring min tx count | `AML_STRUCTURING_MIN_COUNT` | 3 |
| Sender volume spike multiplier | `AML_SENDER_SPIKE_MULT` | 5 |
| Agent volume spike multiplier | `AML_AGENT_SPIKE_MULT` | 3 |
| Beneficiary concentration distinct senders | `AML_BENEFICIARY_CONCENTRATION_COUNT` | 10 |
| Hourly velocity count | `AML_HOURLY_VELOCITY_COUNT` | 5 |
| Night-time structuring count | `AML_NIGHTTIME_COUNT` | 3 |
| New-account age threshold (days) | `AML_NEW_ACCOUNT_DAYS` | 1 |
| New-account high-volume threshold | `AML_NEW_ACCOUNT_VOLUME_USD` | 500 |
| Risk level thresholds | `AML_RISK_MEDIUM`/`AML_RISK_HIGH`/`AML_RISK_CRITICAL` | 30/60/80 |
| Severity → points | `AML_SEVERITY_LOW_PTS`/`MEDIUM_PTS`/`HIGH_PTS` | 5/15/35 |

**Not building**: PEP/sanctions screening. The prototype's version is a
hardcoded 3-phone-prefix blocklist — not real screening, actively worse than
nothing (false confidence). This is a named, deferred integration point for a
real provider (ComplyAdvantage/WorldCheck or similar) later — same treatment
as the SMS provider decision.

## Payment-side integration (part of the payment work, not the compliance
service build)

On relevant transfer transitions (at minimum: `→ done`; arguably also
`→ pending_client_transfer`/creation, since the daily-limit/structuring rules
are most useful *before* money moves, but v1 stays detection-only/
post-hoc per the prototype's own "recommendation, not a block" framing —
revisit blocking behavior later if wanted): `payment`'s transfer usecase
computes the metrics above from its own `transfers`/`ledger_lines` tables and
enqueues a River job (same fire-and-forget pattern as the existing `Notify`
job) that calls `ComplianceService.EvaluateTransfer`. Never blocks the
transfer flow — a compliance-evaluation failure is logged, not propagated.

## Admin app

New `mokapay-admin` page: alerts list (filter by status/severity) + case list
+ a simple dashboard (counts) — the equivalent of the prototype's
`Compliance.jsx` idea, own implementation.
