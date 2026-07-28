package mapper

import (
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
	transfermapper "mokapay.app/app/services/payment/internal/api/transfer/mapper"
	"mokapay.app/app/services/payment/internal/business/commission"
	paymentv1 "mokapay.app/proto/codebase/payment/v1"
)

var commissionStatusFromProto = map[paymentv1.CommissionStatus]commission.Status{
	paymentv1.CommissionStatus_COMMISSION_STATUS_ACCRUED: commission.StatusAccrued,
	paymentv1.CommissionStatus_COMMISSION_STATUS_BATCHED: commission.StatusBatched,
	paymentv1.CommissionStatus_COMMISSION_STATUS_PAID:    commission.StatusPaid,
}

var commissionStatusToProto = map[commission.Status]paymentv1.CommissionStatus{
	commission.StatusAccrued: paymentv1.CommissionStatus_COMMISSION_STATUS_ACCRUED,
	commission.StatusBatched: paymentv1.CommissionStatus_COMMISSION_STATUS_BATCHED,
	commission.StatusPaid:    paymentv1.CommissionStatus_COMMISSION_STATUS_PAID,
}

// CommissionStatusFromProto returns "" for UNSPECIFIED — an absent filter.
func CommissionStatusFromProto(s paymentv1.CommissionStatus) commission.Status {
	return commissionStatusFromProto[s]
}

func CommissionStatusToProto(s commission.Status) paymentv1.CommissionStatus {
	return commissionStatusToProto[s]
}

func CommissionToProto(in commission.Commission) *paymentv1.Commission {
	out := &paymentv1.Commission{
		Id:                  in.ID.String(),
		TransferId:          in.TransferID.String(),
		TransferReference:   in.TransferReference,
		AgentProfileId:      in.AgentProfileID.String(),
		FeeCurrency:         transfermapper.CurrencyToProto(in.FeeCurrency),
		FeeAmount:           in.FeeAmount,
		AgentAmount:         in.AgentAmount,
		PlatformAmount:      in.PlatformAmount,
		GuaranteeFundAmount: in.GuaranteeFundAmount,
		MarginCurrency:      transfermapper.CurrencyToProto(in.MarginCurrency),
		MarginAmount:        in.MarginAmount,
		PayableAmount:       in.PayableAmount,
		AccountingWeek:      in.AccountingWeek.Format(time.DateOnly),
		Status:              CommissionStatusToProto(in.Status),
		CreatedAt:           timestamppb.New(in.CreatedAt),
		UpdatedAt:           timestamppb.New(in.UpdatedAt),
	}
	if in.BatchID != nil {
		out.BatchId = new(in.BatchID.String())
	}

	return out
}

func CommissionBatchToProto(in commission.Batch) *paymentv1.CommissionBatch {
	return &paymentv1.CommissionBatch{
		Id:               in.ID.String(),
		AgentProfileId:   in.AgentProfileID.String(),
		AccountingWeek:   in.AccountingWeek.Format(time.DateOnly),
		CommissionCount:  in.CommissionCount,
		TotalAmount:      in.TotalAmount,
		LedgerRequestId:  in.LedgerRequestID.String(),
		LedgerRequestRef: in.LedgerRequestRef,
		Status:           CommissionStatusToProto(in.Status),
		CreatedAt:        timestamppb.New(in.CreatedAt),
		UpdatedAt:        timestamppb.New(in.UpdatedAt),
	}
}
