package mapper

import (
	"google.golang.org/protobuf/types/known/timestamppb"
	"mokapay.app/app/services/payment/internal/business/dispute"
	paymentv1 "mokapay.app/proto/codebase/payment/v1"
)

// --- enums -----------------------------------------------------------------

var disputePartyToProto = map[dispute.Party]paymentv1.DisputeParty{
	dispute.PartyClient: paymentv1.DisputeParty_DISPUTE_PARTY_CLIENT,
	dispute.PartyAgent:  paymentv1.DisputeParty_DISPUTE_PARTY_AGENT,
	dispute.PartySystem: paymentv1.DisputeParty_DISPUTE_PARTY_SYSTEM,
}

func DisputePartyToProto(p dispute.Party) paymentv1.DisputeParty {
	return disputePartyToProto[p]
}

var disputeReasonFromProto = map[paymentv1.DisputeReason]dispute.Reason{
	paymentv1.DisputeReason_DISPUTE_REASON_AGENT_NOT_PAID:         dispute.ReasonAgentNotPaid,
	paymentv1.DisputeReason_DISPUTE_REASON_RECIPIENT_NOT_RECEIVED: dispute.ReasonRecipientNotReceived,
	paymentv1.DisputeReason_DISPUTE_REASON_CLIENT_FRAUD:           dispute.ReasonClientFraud,
	paymentv1.DisputeReason_DISPUTE_REASON_OTHER:                  dispute.ReasonOther,
}

var disputeReasonToProto = map[dispute.Reason]paymentv1.DisputeReason{
	dispute.ReasonAgentNotPaid:         paymentv1.DisputeReason_DISPUTE_REASON_AGENT_NOT_PAID,
	dispute.ReasonRecipientNotReceived: paymentv1.DisputeReason_DISPUTE_REASON_RECIPIENT_NOT_RECEIVED,
	dispute.ReasonClientFraud:          paymentv1.DisputeReason_DISPUTE_REASON_CLIENT_FRAUD,
	dispute.ReasonOther:                paymentv1.DisputeReason_DISPUTE_REASON_OTHER,
}

// DisputeReasonFromProto returns "" for UNSPECIFIED — the handler rejects it.
func DisputeReasonFromProto(r paymentv1.DisputeReason) dispute.Reason {
	return disputeReasonFromProto[r]
}

func DisputeReasonToProto(r dispute.Reason) paymentv1.DisputeReason {
	return disputeReasonToProto[r]
}

var disputeStatusFromProto = map[paymentv1.DisputeStatus]dispute.Status{
	paymentv1.DisputeStatus_DISPUTE_STATUS_OPEN:     dispute.StatusOpen,
	paymentv1.DisputeStatus_DISPUTE_STATUS_RESOLVED: dispute.StatusResolved,
}

var disputeStatusToProto = map[dispute.Status]paymentv1.DisputeStatus{
	dispute.StatusOpen:     paymentv1.DisputeStatus_DISPUTE_STATUS_OPEN,
	dispute.StatusResolved: paymentv1.DisputeStatus_DISPUTE_STATUS_RESOLVED,
}

func DisputeStatusFromProto(s paymentv1.DisputeStatus) dispute.Status {
	return disputeStatusFromProto[s]
}

func DisputeStatusToProto(s dispute.Status) paymentv1.DisputeStatus {
	return disputeStatusToProto[s]
}

var resolutionFromProto = map[paymentv1.DisputeResolution]dispute.Resolution{
	paymentv1.DisputeResolution_DISPUTE_RESOLUTION_AGENT_RIGHT:  dispute.ResolutionAgentRight,
	paymentv1.DisputeResolution_DISPUTE_RESOLUTION_AGENT_FAULT:  dispute.ResolutionAgentFault,
	paymentv1.DisputeResolution_DISPUTE_RESOLUTION_CLIENT_FRAUD: dispute.ResolutionClientFraud,
	paymentv1.DisputeResolution_DISPUTE_RESOLUTION_NO_FAULT:     dispute.ResolutionNoFault,
}

var resolutionToProto = map[dispute.Resolution]paymentv1.DisputeResolution{
	dispute.ResolutionAgentRight:  paymentv1.DisputeResolution_DISPUTE_RESOLUTION_AGENT_RIGHT,
	dispute.ResolutionAgentFault:  paymentv1.DisputeResolution_DISPUTE_RESOLUTION_AGENT_FAULT,
	dispute.ResolutionClientFraud: paymentv1.DisputeResolution_DISPUTE_RESOLUTION_CLIENT_FRAUD,
	dispute.ResolutionNoFault:     paymentv1.DisputeResolution_DISPUTE_RESOLUTION_NO_FAULT,
}

// ResolutionFromProto returns "" for UNSPECIFIED — the usecase rejects it
// through dispute.EffectOf.
func ResolutionFromProto(r paymentv1.DisputeResolution) dispute.Resolution {
	return resolutionFromProto[r]
}

func ResolutionToProto(r dispute.Resolution) paymentv1.DisputeResolution {
	return resolutionToProto[r]
}

var holdOutcomeToProto = map[dispute.HoldOutcome]paymentv1.HoldOutcome{
	dispute.HoldReleased: paymentv1.HoldOutcome_HOLD_OUTCOME_RELEASED,
	dispute.HoldCaptured: paymentv1.HoldOutcome_HOLD_OUTCOME_CAPTURED,
}

func HoldOutcomeToProto(o dispute.HoldOutcome) paymentv1.HoldOutcome {
	return holdOutcomeToProto[o]
}

// --- messages --------------------------------------------------------------

func DisputeToProto(in dispute.Dispute) *paymentv1.Dispute {
	out := &paymentv1.Dispute{
		Id:                in.ID.String(),
		TransferId:        in.TransferID.String(),
		TransferReference: in.TransferReference,
		ClientId:          in.ClientID.String(),
		OpenedByParty:     DisputePartyToProto(in.OpenedByParty),
		Reason:            DisputeReasonToProto(in.Reason),
		Details:           in.Details,
		FromStatus:        StatusToProto(in.FromStatus),
		HoldAmount:        in.HoldAmount,
		RefundCurrency:    CurrencyToProto(in.RefundCurrency),
		RefundAmount:      in.RefundAmount,
		Status:            DisputeStatusToProto(in.Status),
		ResolutionNote:    in.ResolutionNote,
		CreatedAt:         timestamppb.New(in.CreatedAt),
		UpdatedAt:         timestamppb.New(in.UpdatedAt),
	}
	if in.AgentProfileID != nil {
		out.AgentProfileId = new(in.AgentProfileID.String())
	}
	if in.OpenedBy != nil {
		out.OpenedBy = new(in.OpenedBy.String())
	}
	if in.Resolution != nil {
		out.Resolution = new(ResolutionToProto(*in.Resolution))
	}
	if in.HoldOutcome != nil {
		out.HoldOutcome = new(HoldOutcomeToProto(*in.HoldOutcome))
	}
	if in.DeductionID != nil {
		out.DeductionLedgerRequestId = new(in.DeductionID.String())
	}
	if in.ResolvedBy != nil {
		out.ResolvedBy = new(in.ResolvedBy.String())
	}
	if in.ResolvedAt != nil {
		out.ResolvedAt = timestamppb.New(*in.ResolvedAt)
	}

	return out
}
