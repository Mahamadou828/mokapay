// Package grpcerr translates business errors into gRPC statuses. Every
// handler funnels usecase errors through Map so the wire codes stay
// consistent across services and unknown errors never leak internals.
package grpcerr

import (
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"mokapay.app/app/services/compliance/internal/business/evaluation"
	"mokapay.app/app/services/compliance/internal/business/investigation"
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
	case errors.Is(err, evaluation.ErrAlertNotFound),
		errors.Is(err, investigation.ErrCaseNotFound),
		errors.Is(err, investigation.ErrLinkTargetMissing):
		return status.Error(codes.NotFound, err.Error())

	case errors.Is(err, evaluation.ErrInvalidStatus),
		errors.Is(err, investigation.ErrInvalidStatus):
		return status.Error(codes.InvalidArgument, err.Error())

	case errors.Is(err, evaluation.ErrResolutionRequired):
		return status.Error(codes.FailedPrecondition, err.Error())
	}

	// Unknown failure: don't leak internals on the wire — the usecase
	// already logged the real error.
	return status.Error(codes.Internal, "internal error")
}
