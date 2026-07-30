// Package evaluation holds the AML domain types: the immutable rule
// evaluation (the audit log), the alert built on top of a triggered one,
// and the derived account risk. The rule logic itself lives in
// evaluation/usecase; persistence in evaluation/repo.
package evaluation

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrAlertNotFound covers an unknown id and a soft-deleted one alike.
var ErrAlertNotFound = errors.New("alert not found")

// ErrInvalidStatus rejects a status value outside the lifecycle.
var ErrInvalidStatus = errors.New("invalid alert status")

// ErrResolutionRequired rejects closing an alert with no explanation — a
// resolved or dismissed alert always says why.
var ErrResolutionRequired = errors.New("resolution note required to close an alert")

// SubjectType values mirror the stored compliance_subject_type enum.
type SubjectType string

const (
	SubjectSender    SubjectType = "sender"
	SubjectAgent     SubjectType = "agent"
	SubjectRecipient SubjectType = "recipient"
)

// RuleCode values mirror the stored compliance_rule_code enum — the fixed
// rule set. Adding one is a migration, never a config toggle.
type RuleCode string

const (
	RuleDailyVolumeLimit         RuleCode = "daily_volume_limit"
	RuleWeeklyStructuring        RuleCode = "weekly_structuring"
	RuleSenderVolumeSpike        RuleCode = "sender_volume_spike"
	RuleAgentVolumeSpike         RuleCode = "agent_volume_spike"
	RuleBeneficiaryConcentration RuleCode = "beneficiary_concentration"
	RuleHourlyVelocity           RuleCode = "hourly_velocity"
	RuleNightTimeStructuring     RuleCode = "night_time_structuring"
	RuleNewAccountHighVolume     RuleCode = "new_account_high_volume"
)

// Severity values mirror the stored compliance_severity enum.
type Severity string

const (
	SeverityLow    Severity = "low"
	SeverityMedium Severity = "medium"
	SeverityHigh   Severity = "high"
)

// AlertStatus values mirror the stored compliance_alert_status enum.
type AlertStatus string

const (
	AlertOpen        AlertStatus = "open"
	AlertUnderReview AlertStatus = "under_review"
	AlertEscalated   AlertStatus = "escalated"
	AlertResolved    AlertStatus = "resolved"
	AlertDismissed   AlertStatus = "dismissed"
)

// IsClosing reports whether the status ends the review — those demand a
// resolution note.
func (s AlertStatus) IsClosing() bool {
	return s == AlertResolved || s == AlertDismissed
}

// RiskLevel is the band a risk score falls into. Deliberately NOT a
// database enum: the score is computed on read and the bands come from
// env config, so ops retune them without a migration.
type RiskLevel string

const (
	RiskLow      RiskLevel = "low"
	RiskMedium   RiskLevel = "medium"
	RiskHigh     RiskLevel = "high"
	RiskCritical RiskLevel = "critical"
)

// RuleEvaluation is one immutable audit row: this rule, against these
// inputs, at this time. Never updated, never deleted.
type RuleEvaluation struct {
	ID             uuid.UUID
	TransferID     uuid.UUID // payment NK
	RuleCode       RuleCode
	SubjectType    SubjectType
	AccountID      *uuid.UUID // sender/agent subjects
	RecipientPhone *string    // recipient subjects
	Triggered      bool
	Severity       *Severity // nil when not triggered
	RiskPoints     int32     // snapshot: points assigned at evaluation time
	Inputs         json.RawMessage
	EvaluatedAt    time.Time
}

// NewRuleEvaluation is the append payload — the only shape that reaches
// the audit log. Produced by the rule engine, never by a wire caller.
type NewRuleEvaluation struct {
	TransferID     uuid.UUID
	RuleCode       RuleCode
	SubjectType    SubjectType
	AccountID      *uuid.UUID
	RecipientPhone *string
	Triggered      bool
	Severity       *Severity
	RiskPoints     int32
	Inputs         json.RawMessage
}

// Alert is a triggered evaluation plus its staff review lifecycle. The
// rule/subject fields are joined from the evaluation for display.
type Alert struct {
	ID               uuid.UUID
	RuleEvaluationID uuid.UUID
	Status           AlertStatus
	AssigneeID       *uuid.UUID
	ResolutionNote   *string
	RuleCode         RuleCode
	SubjectType      SubjectType
	Severity         *Severity
	TransferID       uuid.UUID
	AccountID        *uuid.UUID
	RecipientPhone   *string
	RiskPoints       int32
	EvaluatedAt      time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// UpdateAlert is the review payload: the status move IS the operation, so
// it is required; the rest is optional (nil = don't change).
type UpdateAlert struct {
	ID             uuid.UUID
	Status         AlertStatus
	AssigneeID     *uuid.UUID
	ResolutionNote *string
}

// AlertFilter narrows the worklist; nil fields mean "not filtering".
type AlertFilter struct {
	Status    *AlertStatus
	Severity  *Severity
	AccountID *uuid.UUID
}

// AccountRisk is the 90-day rolling score of one account plus the band it
// falls into. Computed on read — never a stored running total.
type AccountRisk struct {
	AccountID       uuid.UUID
	Score           int64
	Level           RiskLevel
	LastEvaluatedAt *time.Time // nil when the account has no evaluation in the window
}

// Dashboard carries the counts behind the admin compliance page.
type Dashboard struct {
	OpenAlerts           int64
	UnderReviewAlerts    int64
	EscalatedAlerts      int64
	HighSeverityAlerts   int64
	MediumSeverityAlerts int64
	LowSeverityAlerts    int64
	FlaggedTransfers     int64
	OpenCases            int64
	InvestigatingCases   int64
	SARCases             int64
}
