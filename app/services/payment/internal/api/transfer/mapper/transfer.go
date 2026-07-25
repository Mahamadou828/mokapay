// Package mapper converts between the transfer protos and the business
// types (free functions, both directions). The agent api reuses the
// enum and transfer conversions from here.
package mapper

import (
	"fmt"

	"google.golang.org/protobuf/types/known/timestamppb"
	"mokapay.app/app/services/payment/internal/business/agentwallet"
	"mokapay.app/app/services/payment/internal/business/beneficiary"
	"mokapay.app/app/services/payment/internal/business/matching"
	"mokapay.app/app/services/payment/internal/business/transfer"
	"mokapay.app/app/services/payment/internal/business/transfer/usecase"
	paymentv1 "mokapay.app/proto/codebase/payment/v1"
)

// --- enums -----------------------------------------------------------------

var currencyFromProto = map[paymentv1.Currency]transfer.Currency{
	paymentv1.Currency_CURRENCY_USD: transfer.CurrencyUSD,
	paymentv1.Currency_CURRENCY_CDF: transfer.CurrencyCDF,
}

var currencyToProto = map[transfer.Currency]paymentv1.Currency{
	transfer.CurrencyUSD: paymentv1.Currency_CURRENCY_USD,
	transfer.CurrencyCDF: paymentv1.Currency_CURRENCY_CDF,
}

func CurrencyFromProto(c paymentv1.Currency) transfer.Currency {
	return currencyFromProto[c] // zero value = invalid, vetoed by the usecase
}

func CurrencyToProto(c transfer.Currency) paymentv1.Currency {
	return currencyToProto[c]
}

var operatorFromProto = map[paymentv1.Operator]transfer.Operator{
	paymentv1.Operator_OPERATOR_ORANGE_MONEY: transfer.OperatorOrangeMoney,
	paymentv1.Operator_OPERATOR_M_PESA:       transfer.OperatorMPesa,
	paymentv1.Operator_OPERATOR_AIRTEL_MONEY: transfer.OperatorAirtelMoney,
}

var operatorToProto = map[transfer.Operator]paymentv1.Operator{
	transfer.OperatorOrangeMoney: paymentv1.Operator_OPERATOR_ORANGE_MONEY,
	transfer.OperatorMPesa:       paymentv1.Operator_OPERATOR_M_PESA,
	transfer.OperatorAirtelMoney: paymentv1.Operator_OPERATOR_AIRTEL_MONEY,
}

func OperatorFromProto(o paymentv1.Operator) transfer.Operator {
	return operatorFromProto[o]
}

func OperatorToProto(o transfer.Operator) paymentv1.Operator {
	return operatorToProto[o]
}

var statusFromProto = map[paymentv1.TransferStatus]transfer.Status{
	paymentv1.TransferStatus_TRANSFER_STATUS_PENDING_MATCH:               transfer.StatusPendingMatch,
	paymentv1.TransferStatus_TRANSFER_STATUS_PENDING_AGENT_APPROVAL:      transfer.StatusPendingAgentApproval,
	paymentv1.TransferStatus_TRANSFER_STATUS_PENDING_CLIENT_TRANSFER:     transfer.StatusPendingClientTransfer,
	paymentv1.TransferStatus_TRANSFER_STATUS_PENDING_AGENT_TRANSFER:      transfer.StatusPendingAgentTransfer,
	paymentv1.TransferStatus_TRANSFER_STATUS_PENDING_CLIENT_CONFIRMATION: transfer.StatusPendingClientConfirmation,
	paymentv1.TransferStatus_TRANSFER_STATUS_DONE:                        transfer.StatusDone,
	paymentv1.TransferStatus_TRANSFER_STATUS_CANCELLED:                   transfer.StatusCancelled,
	paymentv1.TransferStatus_TRANSFER_STATUS_EXPIRED:                     transfer.StatusExpired,
	paymentv1.TransferStatus_TRANSFER_STATUS_DISPUTED:                    transfer.StatusDisputed,
}

var statusToProto = map[transfer.Status]paymentv1.TransferStatus{
	transfer.StatusPendingMatch:              paymentv1.TransferStatus_TRANSFER_STATUS_PENDING_MATCH,
	transfer.StatusPendingAgentApproval:      paymentv1.TransferStatus_TRANSFER_STATUS_PENDING_AGENT_APPROVAL,
	transfer.StatusPendingClientTransfer:     paymentv1.TransferStatus_TRANSFER_STATUS_PENDING_CLIENT_TRANSFER,
	transfer.StatusPendingAgentTransfer:      paymentv1.TransferStatus_TRANSFER_STATUS_PENDING_AGENT_TRANSFER,
	transfer.StatusPendingClientConfirmation: paymentv1.TransferStatus_TRANSFER_STATUS_PENDING_CLIENT_CONFIRMATION,
	transfer.StatusDone:                      paymentv1.TransferStatus_TRANSFER_STATUS_DONE,
	transfer.StatusCancelled:                 paymentv1.TransferStatus_TRANSFER_STATUS_CANCELLED,
	transfer.StatusExpired:                   paymentv1.TransferStatus_TRANSFER_STATUS_EXPIRED,
	transfer.StatusDisputed:                  paymentv1.TransferStatus_TRANSFER_STATUS_DISPUTED,
}

func StatusFromProto(s paymentv1.TransferStatus) transfer.Status {
	return statusFromProto[s]
}

func StatusToProto(s transfer.Status) paymentv1.TransferStatus {
	return statusToProto[s]
}

var offerStatusFromProto = map[paymentv1.OfferStatus]matching.OfferStatus{
	paymentv1.OfferStatus_OFFER_STATUS_OFFERED:  matching.OfferStatusOffered,
	paymentv1.OfferStatus_OFFER_STATUS_ACCEPTED: matching.OfferStatusAccepted,
	paymentv1.OfferStatus_OFFER_STATUS_DECLINED: matching.OfferStatusDeclined,
	paymentv1.OfferStatus_OFFER_STATUS_VOIDED:   matching.OfferStatusVoided,
	paymentv1.OfferStatus_OFFER_STATUS_EXPIRED:  matching.OfferStatusExpired,
}

var offerStatusToProto = map[matching.OfferStatus]paymentv1.OfferStatus{
	matching.OfferStatusOffered:  paymentv1.OfferStatus_OFFER_STATUS_OFFERED,
	matching.OfferStatusAccepted: paymentv1.OfferStatus_OFFER_STATUS_ACCEPTED,
	matching.OfferStatusDeclined: paymentv1.OfferStatus_OFFER_STATUS_DECLINED,
	matching.OfferStatusVoided:   paymentv1.OfferStatus_OFFER_STATUS_VOIDED,
	matching.OfferStatusExpired:  paymentv1.OfferStatus_OFFER_STATUS_EXPIRED,
}

func OfferStatusFromProto(s paymentv1.OfferStatus) matching.OfferStatus {
	return offerStatusFromProto[s]
}

func OfferStatusToProto(s matching.OfferStatus) paymentv1.OfferStatus {
	return offerStatusToProto[s]
}

// --- messages ----------------------------------------------------------------

// TransferToProto renders the transfer; the payment details ride only on
// views the usecase populated them for (the owner, once matched).
func TransferToProto(v usecase.TransferView) *paymentv1.Transfer {
	t := v.Transfer
	out := &paymentv1.Transfer{
		Id:                t.ID.String(),
		Reference:         t.Reference,
		ClientId:          t.ClientID.String(),
		RecipientName:     t.RecipientName,
		RecipientPhone:    t.RecipientPhone,
		RecipientOperator: OperatorToProto(t.RecipientOperator),
		RecipientCity:     t.RecipientCity,
		FromCurrency:      CurrencyToProto(t.FromCurrency),
		FromOperator:      OperatorToProto(t.FromOperator),
		FromAmount:        t.FromAmount,
		ToCurrency:        CurrencyToProto(t.ToCurrency),
		ToAmount:          t.ToAmount,
		FeePct:            t.FeePct,
		FeeAmount:         t.FeeAmount,
		MarginAmount:      t.MarginAmount,
		TotalAmount:       t.TotalAmount,
		Rate:              t.Rate,
		HoldAmount:        t.HoldAmount,
		Note:              t.Note,
		Status:            StatusToProto(t.Status),
		MatchRound:        t.MatchRound,
		CreatedAt:         timestamppb.New(t.CreatedAt),
		UpdatedAt:         timestamppb.New(t.UpdatedAt),
	}
	if t.BeneficiaryID != nil {
		out.BeneficiaryId = new(t.BeneficiaryID.String())
	}
	if t.AgentProfileID != nil {
		out.AgentProfileId = new(t.AgentProfileID.String())
	}
	if t.ProofClientID != nil {
		out.ProofClientFileId = new(t.ProofClientID.String())
	}
	if t.ProofAgentID != nil {
		out.ProofAgentFileId = new(t.ProofAgentID.String())
	}
	if v.PaymentDetails != nil {
		out.AgentPaymentDetails = &paymentv1.AgentPaymentDetails{
			Operator: OperatorToProto(v.PaymentDetails.Operator),
			Msisdn:   v.PaymentDetails.Msisdn,
		}
	}

	return out
}

func QuoteToProto(q usecase.Quote) *paymentv1.Quote {
	out := &paymentv1.Quote{
		FromCurrency: CurrencyToProto(q.FromCurrency),
		FromAmount:   q.FromAmount,
		ToCurrency:   CurrencyToProto(q.ToCurrency),
		ToAmount:     q.ToAmount,
		FeePct:       bpsToPct(q.FeePctBps),
		FeeAmount:    q.FeeAmount,
		MarginAmount: q.MarginAmount,
		TotalAmount:  q.TotalAmount,
	}
	if q.RateMicros != nil {
		out.Rate = new(microsToDecimal(*q.RateMicros))
	}
	return out
}

func EventToProto(e transfer.Event) *paymentv1.TransferEvent {
	out := &paymentv1.TransferEvent{
		Id:         e.ID.String(),
		TransferId: e.TransferID.String(),
		ToStatus:   StatusToProto(e.ToStatus),
		Reason:     e.Reason,
		CreatedAt:  timestamppb.New(e.CreatedAt),
	}
	if e.ActorID != nil {
		out.ActorId = new(e.ActorID.String())
	}
	if e.FromStatus != nil {
		out.FromStatus = new(StatusToProto(*e.FromStatus))
	}
	return out
}

func BeneficiaryToProto(b beneficiary.Beneficiary) *paymentv1.Beneficiary {
	return &paymentv1.Beneficiary{
		Id:        b.ID.String(),
		ClientId:  b.ClientID.String(),
		Name:      b.Name,
		Phone:     b.Phone,
		Operator:  OperatorToProto(b.Operator),
		City:      b.City,
		CreatedAt: timestamppb.New(b.CreatedAt),
		UpdatedAt: timestamppb.New(b.UpdatedAt),
	}
}

func WalletToProto(w agentwallet.Wallet) *paymentv1.AgentWallet {
	return &paymentv1.AgentWallet{
		Id:             w.ID.String(),
		AgentProfileId: w.AgentProfileID.String(),
		Operator:       OperatorToProto(w.Operator),
		Currency:       CurrencyToProto(w.Currency),
		Msisdn:         w.Msisdn,
		CreatedAt:      timestamppb.New(w.CreatedAt),
		UpdatedAt:      timestamppb.New(w.UpdatedAt),
	}
}

func OfferToProto(o matching.AgentOffer) *paymentv1.Offer {
	out := &paymentv1.Offer{
		Id:                o.ID.String(),
		TransferId:        o.TransferID.String(),
		AgentProfileId:    o.AgentProfileID.String(),
		MatchRound:        o.MatchRound,
		Score:             o.Score,
		Status:            OfferStatusToProto(o.Status),
		ExpiresAt:         timestamppb.New(o.ExpiresAt),
		CreatedAt:         timestamppb.New(o.CreatedAt),
		TransferReference: o.TransferReference,
		FromCurrency:      CurrencyToProto(o.FromCurrency),
		FromOperator:      OperatorToProto(o.FromOperator),
		FromAmount:        o.FromAmount,
		ToCurrency:        CurrencyToProto(o.ToCurrency),
		RecipientOperator: OperatorToProto(o.RecipientOperator),
		RecipientCity:     o.RecipientCity,
		ToAmount:          o.ToAmount,
		TotalAmount:       o.TotalAmount,
		HoldAmount:        o.HoldAmount,
		Note:              o.Note,
	}
	if o.DecidedAt != nil {
		out.DecidedAt = timestamppb.New(*o.DecidedAt)
	}
	return out
}

// bpsToPct renders basis points as "1.50".
func bpsToPct(bps int32) string {
	return fmt.Sprintf("%d.%02d", bps/100, bps%100)
}

// microsToDecimal renders a micros rate as "2830.500000".
func microsToDecimal(micros int64) string {
	return fmt.Sprintf("%d.%06d", micros/1_000_000, micros%1_000_000)
}
