// Package mapper converts between the evaluation business types and their
// proto counterparts.
package mapper

import (
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
	"mokapay.app/app/services/compliance/internal/business/evaluation"
	compliancev1 "mokapay.app/proto/codebase/compliance/v1"
)

// SnapshotFromProto builds the evaluation input from the request. Every
// id the caller sends is parsed here — a malformed one is a bad request,
// never a zero uuid silently written into the audit log.
func SnapshotFromProto(in *compliancev1.EvaluateTransferRequest) (evaluation.Snapshot, error) {
	transferID, err := uuid.Parse(in.TransferId)
	if err != nil {
		return evaluation.Snapshot{}, err
	}

	sender, err := AccountMetricsFromProto(in.Sender)
	if err != nil {
		return evaluation.Snapshot{}, err
	}

	out := evaluation.Snapshot{
		TransferID: transferID,
		Sender:     sender,
		Recipient: evaluation.RecipientMetrics{
			Phone:             in.Recipient.GetPhone(),
			DistinctSenders7d: in.Recipient.GetDistinctSenders_7D(),
		},
	}

	// The agent block is optional: a transfer evaluated before matching
	// has none.
	if in.Agent != nil {
		agent, err := AccountMetricsFromProto(in.Agent)
		if err != nil {
			return evaluation.Snapshot{}, err
		}
		out.Agent = new(agent)
	}

	return out, nil
}

func AccountMetricsFromProto(in *compliancev1.TransferMetrics) (evaluation.AccountMetrics, error) {
	accountID, err := uuid.Parse(in.GetAccountId())
	if err != nil {
		return evaluation.AccountMetrics{}, err
	}

	return evaluation.AccountMetrics{
		AccountID:                   accountID,
		CurrentAmountUSD:            in.GetCurrentAmountUsd(),
		Rolling24hVolumeUSD:         in.GetRolling_24HVolumeUsd(),
		Rolling7dVolumeUSD:          in.GetRolling_7DVolumeUsd(),
		Rolling7dCount:              in.GetRolling_7DCount(),
		Rolling30dAvgDailyVolumeUSD: in.GetRolling_30DAvgDailyVolumeUsd(),
		Rolling1hCount:              in.GetRolling_1HCount(),
		Rolling7dNighttimeCount:     in.GetRolling_7DNighttimeCount(),
		AccountAgeDays:              in.GetAccountAgeDays(),
	}, nil
}

func RuleEvaluationToProto(in evaluation.RuleEvaluation) *compliancev1.RuleEvaluation {
	out := &compliancev1.RuleEvaluation{
		Id:          in.ID.String(),
		TransferId:  in.TransferID.String(),
		RuleCode:    RuleCodeToProto(in.RuleCode),
		SubjectType: SubjectTypeToProto(in.SubjectType),
		Triggered:   in.Triggered,
		RiskPoints:  in.RiskPoints,
		Inputs:      string(in.Inputs),
		EvaluatedAt: timestamppb.New(in.EvaluatedAt),
	}
	if in.AccountID != nil {
		out.AccountId = new(in.AccountID.String())
	}
	if in.RecipientPhone != nil {
		out.RecipientPhone = new(*in.RecipientPhone)
	}
	if in.Severity != nil {
		out.Severity = SeverityToProto(*in.Severity)
	}

	return out
}
