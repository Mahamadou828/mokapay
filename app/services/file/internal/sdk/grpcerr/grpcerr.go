// Package grpcerr translates the file domain's business errors into gRPC
// statuses; unknown errors never leak internals on the wire.
package grpcerr

import (
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"mokapay.app/app/services/file/internal/business/file"
)

// Map converts a business error to a gRPC status error.
func Map(err error) error {
	if err == nil {
		return nil
	}

	// Already a gRPC status (handler-side validation) — pass through.
	var se interface{ GRPCStatus() *status.Status }
	if errors.As(err, &se) {
		return se.GRPCStatus().Err()
	}

	switch {
	case errors.Is(err, file.ErrFileNotFound):
		return status.Error(codes.NotFound, err.Error())

	case errors.Is(err, file.ErrInvalidPurpose),
		errors.Is(err, file.ErrContentTypeNotAllowed),
		errors.Is(err, file.ErrFileTooLarge),
		errors.Is(err, file.ErrSizeMismatch):
		return status.Error(codes.InvalidArgument, err.Error())

	case errors.Is(err, file.ErrNotPending),
		errors.Is(err, file.ErrNotStored),
		errors.Is(err, file.ErrObjectMissing):
		return status.Error(codes.FailedPrecondition, err.Error())
	}

	// The usecase already logged the real error.
	return status.Error(codes.Internal, "internal error")
}
