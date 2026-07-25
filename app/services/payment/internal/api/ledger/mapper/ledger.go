// Package mapper converts between the ledger business types and their
// proto counterparts.
package mapper

import (
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
	"mokapay.app/app/services/payment/internal/business/ledger"
	paymentv1 "mokapay.app/proto/codebase/payment/v1"
)

func LedgerRequestToProto(in ledger.LedgerRequest) *paymentv1.LedgerRequest {
	out := &paymentv1.LedgerRequest{
		Id:             in.ID.String(),
		AgentProfileId: in.AgentProfileID.String(),
		Type:           TypeToProto(in.Type),
		Direction:      DirectionToProto(in.Direction),
		Amount:         in.Amount,
		Status:         StatusToProto(in.Status),
		Reason:         in.Reason,
		CreatedAt:      timestamppb.New(in.CreatedAt),
		UpdatedAt:      timestamppb.New(in.UpdatedAt),
	}
	if in.Reference != nil {
		out.ReferenceEntityType = new(ReferenceTypeToProto(in.Reference.Type))
		out.ReferenceEntityId = new(in.Reference.ID.String())
	}
	if in.ProofFileID != nil {
		out.ProofFileId = new(in.ProofFileID.String())
	}
	if in.DecidedBy != nil {
		out.DecidedBy = new(in.DecidedBy.String())
	}
	if in.DecidedAt != nil {
		out.DecidedAt = timestamppb.New(*in.DecidedAt)
	}

	return out
}

func LineToProto(in ledger.Line) *paymentv1.LedgerLine {
	return &paymentv1.LedgerLine{
		Id:              in.ID.String(),
		AgentProfileId:  in.AgentProfileID.String(),
		LedgerRequestId: in.RequestID.String(),
		Amount:          in.Amount,
		BalanceAfter:    in.BalanceAfter,
		CreatedAt:       timestamppb.New(in.CreatedAt),
	}
}

func TypeToProto(in ledger.Type) paymentv1.LedgerRequestType {
	switch in {
	case ledger.TypeFunding:
		return paymentv1.LedgerRequestType_LEDGER_REQUEST_TYPE_FUNDING
	case ledger.TypeWithdrawal:
		return paymentv1.LedgerRequestType_LEDGER_REQUEST_TYPE_WITHDRAWAL
	case ledger.TypeRefund:
		return paymentv1.LedgerRequestType_LEDGER_REQUEST_TYPE_REFUND
	case ledger.TypeDeduction:
		return paymentv1.LedgerRequestType_LEDGER_REQUEST_TYPE_DEDUCTION
	case ledger.TypeCommission:
		return paymentv1.LedgerRequestType_LEDGER_REQUEST_TYPE_COMMISSION
	}
	return paymentv1.LedgerRequestType_LEDGER_REQUEST_TYPE_UNSPECIFIED
}

// TypeFromProto returns "" for UNSPECIFIED — the usecase rejects it.
func TypeFromProto(in paymentv1.LedgerRequestType) ledger.Type {
	switch in {
	case paymentv1.LedgerRequestType_LEDGER_REQUEST_TYPE_FUNDING:
		return ledger.TypeFunding
	case paymentv1.LedgerRequestType_LEDGER_REQUEST_TYPE_WITHDRAWAL:
		return ledger.TypeWithdrawal
	case paymentv1.LedgerRequestType_LEDGER_REQUEST_TYPE_REFUND:
		return ledger.TypeRefund
	case paymentv1.LedgerRequestType_LEDGER_REQUEST_TYPE_DEDUCTION:
		return ledger.TypeDeduction
	case paymentv1.LedgerRequestType_LEDGER_REQUEST_TYPE_COMMISSION:
		return ledger.TypeCommission
	}
	return ""
}

func StatusToProto(in ledger.Status) paymentv1.LedgerRequestStatus {
	switch in {
	case ledger.StatusInitiated:
		return paymentv1.LedgerRequestStatus_LEDGER_REQUEST_STATUS_INITIATED
	case ledger.StatusPendingAgentAction:
		return paymentv1.LedgerRequestStatus_LEDGER_REQUEST_STATUS_PENDING_AGENT_ACTION
	case ledger.StatusPendingAdminApproval:
		return paymentv1.LedgerRequestStatus_LEDGER_REQUEST_STATUS_PENDING_ADMIN_APPROVAL
	case ledger.StatusDone:
		return paymentv1.LedgerRequestStatus_LEDGER_REQUEST_STATUS_DONE
	case ledger.StatusRejected:
		return paymentv1.LedgerRequestStatus_LEDGER_REQUEST_STATUS_REJECTED
	case ledger.StatusDisputed:
		return paymentv1.LedgerRequestStatus_LEDGER_REQUEST_STATUS_DISPUTED
	}
	return paymentv1.LedgerRequestStatus_LEDGER_REQUEST_STATUS_UNSPECIFIED
}

func StatusFromProto(in paymentv1.LedgerRequestStatus) ledger.Status {
	switch in {
	case paymentv1.LedgerRequestStatus_LEDGER_REQUEST_STATUS_INITIATED:
		return ledger.StatusInitiated
	case paymentv1.LedgerRequestStatus_LEDGER_REQUEST_STATUS_PENDING_AGENT_ACTION:
		return ledger.StatusPendingAgentAction
	case paymentv1.LedgerRequestStatus_LEDGER_REQUEST_STATUS_PENDING_ADMIN_APPROVAL:
		return ledger.StatusPendingAdminApproval
	case paymentv1.LedgerRequestStatus_LEDGER_REQUEST_STATUS_DONE:
		return ledger.StatusDone
	case paymentv1.LedgerRequestStatus_LEDGER_REQUEST_STATUS_REJECTED:
		return ledger.StatusRejected
	case paymentv1.LedgerRequestStatus_LEDGER_REQUEST_STATUS_DISPUTED:
		return ledger.StatusDisputed
	}
	return ""
}

func DirectionToProto(in ledger.Direction) paymentv1.LedgerDirection {
	switch in {
	case ledger.DirectionInflow:
		return paymentv1.LedgerDirection_LEDGER_DIRECTION_INFLOW
	case ledger.DirectionOutflow:
		return paymentv1.LedgerDirection_LEDGER_DIRECTION_OUTFLOW
	}
	return paymentv1.LedgerDirection_LEDGER_DIRECTION_UNSPECIFIED
}

func ReferenceTypeToProto(in ledger.ReferenceType) paymentv1.LedgerReferenceType {
	switch in {
	case ledger.ReferenceEnrollment:
		return paymentv1.LedgerReferenceType_LEDGER_REFERENCE_TYPE_ENROLLMENT
	case ledger.ReferenceTransfer:
		return paymentv1.LedgerReferenceType_LEDGER_REFERENCE_TYPE_TRANSFER
	}
	return paymentv1.LedgerReferenceType_LEDGER_REFERENCE_TYPE_UNSPECIFIED
}

// ReferenceFromProto builds the typed reference; nil when absent.
func ReferenceFromProto(t *paymentv1.LedgerReferenceType, id *string) (*ledger.Reference, error) {
	if t == nil && id == nil {
		return nil, nil
	}
	if t == nil || id == nil {
		return nil, ledger.ErrInvalidType
	}
	rid, err := uuid.Parse(*id)
	if err != nil {
		return nil, err
	}
	var rt ledger.ReferenceType
	switch *t {
	case paymentv1.LedgerReferenceType_LEDGER_REFERENCE_TYPE_ENROLLMENT:
		rt = ledger.ReferenceEnrollment
	case paymentv1.LedgerReferenceType_LEDGER_REFERENCE_TYPE_TRANSFER:
		rt = ledger.ReferenceTransfer
	default:
		return nil, ledger.ErrInvalidType
	}
	return new(ledger.Reference{Type: rt, ID: rid}), nil
}
