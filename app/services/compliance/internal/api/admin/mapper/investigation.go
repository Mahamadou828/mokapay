// Package mapper converts between the investigation (case) business types
// and their proto counterparts. The alert/evaluation side of the admin
// surface maps through the evaluation mapper — one converter per business
// type, wherever it is consumed from.
package mapper

import (
	"google.golang.org/protobuf/types/known/timestamppb"
	"mokapay.app/app/services/compliance/internal/business/investigation"
	compliancev1 "mokapay.app/proto/codebase/compliance/v1"
)

func CaseToProto(in investigation.Case) *compliancev1.Case {
	out := &compliancev1.Case{
		Id:        in.ID.String(),
		Reference: in.Reference,
		Status:    CaseStatusToProto(in.Status),
		CreatedAt: timestamppb.New(in.CreatedAt),
		UpdatedAt: timestamppb.New(in.UpdatedAt),
	}
	if in.AssigneeID != nil {
		out.AssigneeId = new(in.AssigneeID.String())
	}
	if in.Summary != nil {
		out.Summary = new(*in.Summary)
	}
	if in.ClosedAt != nil {
		out.ClosedAt = timestamppb.New(*in.ClosedAt)
	}

	return out
}

func CaseStatusToProto(in investigation.Status) compliancev1.CaseStatus {
	switch in {
	case investigation.StatusOpen:
		return compliancev1.CaseStatus_CASE_STATUS_OPEN
	case investigation.StatusInvestigating:
		return compliancev1.CaseStatus_CASE_STATUS_INVESTIGATING
	case investigation.StatusClosedSAR:
		return compliancev1.CaseStatus_CASE_STATUS_CLOSED_SAR
	case investigation.StatusClosedNoAction:
		return compliancev1.CaseStatus_CASE_STATUS_CLOSED_NO_ACTION
	}
	return compliancev1.CaseStatus_CASE_STATUS_UNSPECIFIED
}

// CaseStatusFromProto returns "" for UNSPECIFIED — the usecase rejects it
// on an update, and a filter simply drops it.
func CaseStatusFromProto(in compliancev1.CaseStatus) investigation.Status {
	switch in {
	case compliancev1.CaseStatus_CASE_STATUS_OPEN:
		return investigation.StatusOpen
	case compliancev1.CaseStatus_CASE_STATUS_INVESTIGATING:
		return investigation.StatusInvestigating
	case compliancev1.CaseStatus_CASE_STATUS_CLOSED_SAR:
		return investigation.StatusClosedSAR
	case compliancev1.CaseStatus_CASE_STATUS_CLOSED_NO_ACTION:
		return investigation.StatusClosedNoAction
	}
	return ""
}
