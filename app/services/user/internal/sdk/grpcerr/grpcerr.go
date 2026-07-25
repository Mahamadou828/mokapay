// Package grpcerr translates business errors into gRPC statuses. Every
// handler funnels usecase errors through Map so the wire codes stay
// consistent across services and unknown errors never leak internals.
package grpcerr

import (
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"mokapay.app/app/services/user/internal/business/auth"
	"mokapay.app/app/services/user/internal/business/enrollment"
	"mokapay.app/app/services/user/internal/business/notification"
	"mokapay.app/core/aws"
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
	case errors.Is(err, auth.ErrInvalidPhoneNumber),
		errors.Is(err, auth.ErrInvalidCode),
		errors.Is(err, aws.ErrPasswordPolicy):
		return status.Error(codes.InvalidArgument, err.Error())

	case errors.Is(err, auth.ErrOtpNotFound),
		errors.Is(err, auth.ErrAccountNotFound),
		errors.Is(err, notification.ErrNotificationNotFound),
		errors.Is(err, enrollment.ErrEnrollmentNotFound):
		return status.Error(codes.NotFound, err.Error())

	case errors.Is(err, enrollment.ErrEnrollmentPending):
		return status.Error(codes.AlreadyExists, err.Error())

	case errors.Is(err, enrollment.ErrNotEligible),
		errors.Is(err, enrollment.ErrTOSVersionMismatch),
		errors.Is(err, enrollment.ErrGuaranteeNotSettled):
		return status.Error(codes.FailedPrecondition, err.Error())

	case errors.Is(err, auth.ErrTooManyAttempts),
		errors.Is(err, aws.ErrTooManyRequests):
		return status.Error(codes.ResourceExhausted, err.Error())

	case errors.Is(err, auth.ErrPhoneNumberTaken),
		errors.Is(err, auth.ErrCognitoSubTaken),
		errors.Is(err, aws.ErrPhoneTaken):
		return status.Error(codes.AlreadyExists, err.Error())

	case errors.Is(err, aws.ErrBadCredentials),
		errors.Is(err, aws.ErrInvalidToken):
		return status.Error(codes.Unauthenticated, err.Error())

	case errors.Is(err, auth.ErrAccountSuspended),
		errors.Is(err, auth.ErrAccountBanned):
		return status.Error(codes.PermissionDenied, err.Error())
	}

	// Unknown failure: don't leak internals on the wire — the usecase
	// already logged the real error.
	return status.Error(codes.Internal, "internal error")
}
