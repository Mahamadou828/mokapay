// Package grpcerr translates business errors into gRPC statuses. Every
// handler funnels usecase errors through Map so the wire codes stay
// consistent across services and unknown errors never leak internals.
package grpcerr

import (
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"mokapay.app/app/services/payment/internal/business/agentprofile"
	"mokapay.app/app/services/payment/internal/business/agentwallet"
	"mokapay.app/app/services/payment/internal/business/beneficiary"
	"mokapay.app/app/services/payment/internal/business/commission"
	"mokapay.app/app/services/payment/internal/business/dispute"
	"mokapay.app/app/services/payment/internal/business/ledger"
	"mokapay.app/app/services/payment/internal/business/matching"
	"mokapay.app/app/services/payment/internal/business/transfer"
)

// Map converts a business error to a gRPC status error. Errors that are
// already a gRPC status pass through untouched; anything unrecognized
// becomes codes.Internal with a generic message.
func Map(err error) error {
	if err == nil {
		return nil
	}

	// Already a gRPC status (e.g. handler-side validation) — pass through.
	var se interface{ GRPCStatus() *status.Status }
	if errors.As(err, &se) {
		return se.GRPCStatus().Err()
	}

	switch {
	case errors.Is(err, agentprofile.ErrProfileNotFound),
		errors.Is(err, ledger.ErrRequestNotFound),
		errors.Is(err, transfer.ErrTransferNotFound),
		errors.Is(err, beneficiary.ErrBeneficiaryNotFound),
		errors.Is(err, agentwallet.ErrWalletNotFound),
		errors.Is(err, matching.ErrOfferGone),
		errors.Is(err, dispute.ErrDisputeNotFound),
		errors.Is(err, commission.ErrCommissionNotFound):
		return status.Error(codes.NotFound, err.Error())

	case errors.Is(err, agentprofile.ErrProfileExists),
		errors.Is(err, ledger.ErrReferenceInUse),
		errors.Is(err, agentwallet.ErrWalletExists),
		errors.Is(err, beneficiary.ErrBeneficiaryExists),
		errors.Is(err, commission.ErrCommissionExists):
		return status.Error(codes.AlreadyExists, err.Error())

	case errors.Is(err, agentprofile.ErrProfileHasFunds),
		errors.Is(err, agentprofile.ErrInvalidTransition),
		errors.Is(err, ledger.ErrNotFeasible),
		errors.Is(err, ledger.ErrLedgerStatusClose),
		errors.Is(err, ledger.ErrInvalidStatusMove),
		errors.Is(err, transfer.ErrInvalidStatusMove),
		errors.Is(err, matching.ErrInsufficientAvailable),
		errors.Is(err, dispute.ErrNotDisputable),
		errors.Is(err, commission.ErrBatchNotCreated),
		errors.Is(err, commission.ErrNotPayable):
		return status.Error(codes.FailedPrecondition, err.Error())

	case errors.Is(err, ledger.ErrInvalidType),
		errors.Is(err, transfer.ErrCorridorNotAllowed),
		errors.Is(err, transfer.ErrInvalidAmount),
		errors.Is(err, transfer.ErrRecipientRequired),
		errors.Is(err, dispute.ErrInvalidResolution):
		return status.Error(codes.InvalidArgument, err.Error())

	case errors.Is(err, ledger.ErrNotAllowed),
		errors.Is(err, transfer.ErrNotAllowed),
		errors.Is(err, dispute.ErrNotAllowed):
		return status.Error(codes.PermissionDenied, err.Error())
	}

	// Unknown failure: don't leak internals on the wire — the usecase
	// already logged the real error.
	return status.Error(codes.Internal, "internal error")
}
