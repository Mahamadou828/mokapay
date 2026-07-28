package mapper

import (
	"mokapay.app/app/services/compliance/internal/business/evaluation"
	compliancev1 "mokapay.app/proto/codebase/compliance/v1"
)

func RuleCodeToProto(in evaluation.RuleCode) compliancev1.RuleCode {
	switch in {
	case evaluation.RuleDailyVolumeLimit:
		return compliancev1.RuleCode_RULE_CODE_DAILY_VOLUME_LIMIT
	case evaluation.RuleWeeklyStructuring:
		return compliancev1.RuleCode_RULE_CODE_WEEKLY_STRUCTURING
	case evaluation.RuleSenderVolumeSpike:
		return compliancev1.RuleCode_RULE_CODE_SENDER_VOLUME_SPIKE
	case evaluation.RuleAgentVolumeSpike:
		return compliancev1.RuleCode_RULE_CODE_AGENT_VOLUME_SPIKE
	case evaluation.RuleBeneficiaryConcentration:
		return compliancev1.RuleCode_RULE_CODE_BENEFICIARY_CONCENTRATION
	case evaluation.RuleHourlyVelocity:
		return compliancev1.RuleCode_RULE_CODE_HOURLY_VELOCITY
	case evaluation.RuleNightTimeStructuring:
		return compliancev1.RuleCode_RULE_CODE_NIGHT_TIME_STRUCTURING
	case evaluation.RuleNewAccountHighVolume:
		return compliancev1.RuleCode_RULE_CODE_NEW_ACCOUNT_HIGH_VOLUME
	}
	return compliancev1.RuleCode_RULE_CODE_UNSPECIFIED
}

func SubjectTypeToProto(in evaluation.SubjectType) compliancev1.SubjectType {
	switch in {
	case evaluation.SubjectSender:
		return compliancev1.SubjectType_SUBJECT_TYPE_SENDER
	case evaluation.SubjectAgent:
		return compliancev1.SubjectType_SUBJECT_TYPE_AGENT
	case evaluation.SubjectRecipient:
		return compliancev1.SubjectType_SUBJECT_TYPE_RECIPIENT
	}
	return compliancev1.SubjectType_SUBJECT_TYPE_UNSPECIFIED
}

func SeverityToProto(in evaluation.Severity) compliancev1.Severity {
	switch in {
	case evaluation.SeverityLow:
		return compliancev1.Severity_SEVERITY_LOW
	case evaluation.SeverityMedium:
		return compliancev1.Severity_SEVERITY_MEDIUM
	case evaluation.SeverityHigh:
		return compliancev1.Severity_SEVERITY_HIGH
	}
	return compliancev1.Severity_SEVERITY_UNSPECIFIED
}

// SeverityFromProto returns "" for UNSPECIFIED — the caller drops the
// filter rather than matching on an impossible value.
func SeverityFromProto(in compliancev1.Severity) evaluation.Severity {
	switch in {
	case compliancev1.Severity_SEVERITY_LOW:
		return evaluation.SeverityLow
	case compliancev1.Severity_SEVERITY_MEDIUM:
		return evaluation.SeverityMedium
	case compliancev1.Severity_SEVERITY_HIGH:
		return evaluation.SeverityHigh
	}
	return ""
}

func AlertStatusToProto(in evaluation.AlertStatus) compliancev1.AlertStatus {
	switch in {
	case evaluation.AlertOpen:
		return compliancev1.AlertStatus_ALERT_STATUS_OPEN
	case evaluation.AlertUnderReview:
		return compliancev1.AlertStatus_ALERT_STATUS_UNDER_REVIEW
	case evaluation.AlertEscalated:
		return compliancev1.AlertStatus_ALERT_STATUS_ESCALATED
	case evaluation.AlertResolved:
		return compliancev1.AlertStatus_ALERT_STATUS_RESOLVED
	case evaluation.AlertDismissed:
		return compliancev1.AlertStatus_ALERT_STATUS_DISMISSED
	}
	return compliancev1.AlertStatus_ALERT_STATUS_UNSPECIFIED
}

// AlertStatusFromProto returns "" for UNSPECIFIED — the usecase rejects it.
func AlertStatusFromProto(in compliancev1.AlertStatus) evaluation.AlertStatus {
	switch in {
	case compliancev1.AlertStatus_ALERT_STATUS_OPEN:
		return evaluation.AlertOpen
	case compliancev1.AlertStatus_ALERT_STATUS_UNDER_REVIEW:
		return evaluation.AlertUnderReview
	case compliancev1.AlertStatus_ALERT_STATUS_ESCALATED:
		return evaluation.AlertEscalated
	case compliancev1.AlertStatus_ALERT_STATUS_RESOLVED:
		return evaluation.AlertResolved
	case compliancev1.AlertStatus_ALERT_STATUS_DISMISSED:
		return evaluation.AlertDismissed
	}
	return ""
}

func RiskLevelToProto(in evaluation.RiskLevel) compliancev1.RiskLevel {
	switch in {
	case evaluation.RiskLow:
		return compliancev1.RiskLevel_RISK_LEVEL_LOW
	case evaluation.RiskMedium:
		return compliancev1.RiskLevel_RISK_LEVEL_MEDIUM
	case evaluation.RiskHigh:
		return compliancev1.RiskLevel_RISK_LEVEL_HIGH
	case evaluation.RiskCritical:
		return compliancev1.RiskLevel_RISK_LEVEL_CRITICAL
	}
	return compliancev1.RiskLevel_RISK_LEVEL_UNSPECIFIED
}
