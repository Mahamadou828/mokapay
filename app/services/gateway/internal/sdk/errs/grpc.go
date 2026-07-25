package errs

import (
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// FromGRPC maps a downstream gRPC error onto the equivalent ErrCode. The
// mapping is explicit — the local values do NOT mirror grpc codes (NoContent
// shifts everything past Canceled by one).
func FromGRPC(err error) ErrCode {
	switch status.Code(err) {
	case codes.OK:
		return None
	case codes.Canceled:
		return Canceled
	case codes.InvalidArgument:
		return InvalidArgument
	case codes.DeadlineExceeded:
		return DeadlineExceeded
	case codes.NotFound:
		return NotFound
	case codes.AlreadyExists:
		return AlreadyExists
	case codes.PermissionDenied:
		return PermissionDenied
	case codes.ResourceExhausted:
		return ResourceExhausted
	case codes.FailedPrecondition:
		return FailedPrecondition
	case codes.Aborted:
		return Aborted
	case codes.Unauthenticated:
		return Unauthenticated
	}
	// Unknown, Internal, Unavailable, DataLoss, Unimplemented, OutOfRange:
	// nothing actionable for the client — collapse to Internal.
	return Internal
}
