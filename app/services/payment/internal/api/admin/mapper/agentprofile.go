// Package mapper converts between the agent-profile business types and
// their proto counterparts (shared with the future agent-facing api).
package mapper

import (
	"google.golang.org/protobuf/types/known/timestamppb"
	"mokapay.app/app/services/payment/internal/business/agentprofile"
	paymentv1 "mokapay.app/proto/codebase/payment/v1"
)

func AgentProfileToProto(in agentprofile.AgentProfile) *paymentv1.AgentProfile {
	return &paymentv1.AgentProfile{
		Id:             in.ID.String(),
		AccountId:      in.AccountID.String(),
		Status:         StatusToProto(in.Status),
		Level:          LevelToProto(in.Level),
		Currency:       in.Currency,
		Balance:        in.Balance,
		Held:           in.Held,
		Available:      in.Available,
		GuaranteeFloor: in.GuaranteeFloor,
		City:           in.City,
		CreatedAt:      timestamppb.New(in.CreatedAt),
		UpdatedAt:      timestamppb.New(in.UpdatedAt),
	}
}

func StatusToProto(in agentprofile.Status) paymentv1.AgentProfileStatus {
	switch in {
	case agentprofile.StatusPending:
		return paymentv1.AgentProfileStatus_AGENT_PROFILE_STATUS_PENDING
	case agentprofile.StatusActive:
		return paymentv1.AgentProfileStatus_AGENT_PROFILE_STATUS_ACTIVE
	case agentprofile.StatusSuspended:
		return paymentv1.AgentProfileStatus_AGENT_PROFILE_STATUS_SUSPENDED
	}
	return paymentv1.AgentProfileStatus_AGENT_PROFILE_STATUS_UNSPECIFIED
}

func LevelToProto(in agentprofile.Level) paymentv1.AgentLevel {
	switch in {
	case agentprofile.LevelN1:
		return paymentv1.AgentLevel_AGENT_LEVEL_N1
	case agentprofile.LevelN2:
		return paymentv1.AgentLevel_AGENT_LEVEL_N2
	case agentprofile.LevelN3:
		return paymentv1.AgentLevel_AGENT_LEVEL_N3
	case agentprofile.LevelN4:
		return paymentv1.AgentLevel_AGENT_LEVEL_N4
	}
	return paymentv1.AgentLevel_AGENT_LEVEL_UNSPECIFIED
}
