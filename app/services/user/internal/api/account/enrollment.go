package account

import (
	"context"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"mokapay.app/app/services/user/internal/api/account/mapper"
	"mokapay.app/app/services/user/internal/sdk/grpcerr"
	"mokapay.app/core/passport"
	userv1 "mokapay.app/proto/codebase/user/v1"
)

// callerAccountID resolves the calling account from the
// gateway-rehydrated passport.
func callerAccountID(ctx context.Context) (uuid.UUID, error) {
	pass, err := passport.FromContext(ctx)
	if err != nil {
		return uuid.UUID{}, status.Error(codes.Unauthenticated, "no passport in context")
	}
	id, err := uuid.Parse(pass.User.ID)
	if err != nil {
		return uuid.UUID{}, status.Error(codes.Unauthenticated, "passport carries no account id")
	}
	return id, nil
}

func (h UserHandler) GetMyEnrollment(ctx context.Context, _ *userv1.GetMyEnrollmentRequest) (*userv1.Enrollment, error) {
	accountID, err := callerAccountID(ctx)
	if err != nil {
		return nil, err
	}

	e, err := h.enroll.MyEnrollment(ctx, accountID)
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	return mapper.EnrollmentToProto(e), nil
}

func (h UserHandler) AcceptEnrollment(ctx context.Context, req *userv1.AcceptEnrollmentRequest) (*userv1.Enrollment, error) {
	accountID, err := callerAccountID(ctx)
	if err != nil {
		return nil, err
	}

	id, err := uuid.Parse(req.Id)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid enrollment id")
	}
	if req.TosVersion == "" {
		return nil, status.Error(codes.InvalidArgument, "tos_version is required")
	}

	e, err := h.enroll.Accept(ctx, accountID, id, req.TosVersion)
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	return mapper.EnrollmentToProto(e), nil
}

// SelfEnroll is the AG-04 submission: the TOS checkbox and the nearest
// city arrive together, and the terms version is required because the
// acceptance IS this call (there is no later accept step to stamp it).
func (h UserHandler) SelfEnroll(ctx context.Context, req *userv1.SelfEnrollRequest) (*userv1.Enrollment, error) {
	accountID, err := callerAccountID(ctx)
	if err != nil {
		return nil, err
	}

	if req.TosVersion == "" {
		return nil, status.Error(codes.InvalidArgument, "tos_version is required")
	}

	e, err := h.enroll.SelfEnroll(ctx, accountID, mapper.SelfEnrollToDomain(req))
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	return mapper.EnrollmentToProto(e), nil
}

func (h UserHandler) DeclineEnrollment(ctx context.Context, req *userv1.DeclineEnrollmentRequest) (*userv1.Enrollment, error) {
	accountID, err := callerAccountID(ctx)
	if err != nil {
		return nil, err
	}

	id, err := uuid.Parse(req.Id)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid enrollment id")
	}

	e, err := h.enroll.Decline(ctx, accountID, id)
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	return mapper.EnrollmentToProto(e), nil
}
