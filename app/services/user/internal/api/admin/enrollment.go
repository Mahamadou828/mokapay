package admin

import (
	"context"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"mokapay.app/app/services/user/internal/api/account/mapper"
	"mokapay.app/app/services/user/internal/business/enrollment"
	"mokapay.app/app/services/user/internal/sdk/grpcerr"
	"mokapay.app/core/passport"
	userv1 "mokapay.app/proto/codebase/user/v1"
)

// callerAccountID resolves the acting admin from the gateway-rehydrated
// passport — stamped as invited_by / decided_by on the enrollment.
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

func (h Handler) PromoteUserToAgent(ctx context.Context, req *userv1.PromoteUserToAgentRequest) (*userv1.Enrollment, error) {
	admin, err := callerAccountID(ctx)
	if err != nil {
		return nil, err
	}

	accountID, err := uuid.Parse(req.AccountId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid account id")
	}

	e, err := h.enroll.Invite(ctx, accountID, admin)
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	return mapper.EnrollmentToProto(e), nil
}

func (h Handler) ApproveEnrollment(ctx context.Context, req *userv1.ApproveEnrollmentRequest) (*userv1.Enrollment, error) {
	admin, err := callerAccountID(ctx)
	if err != nil {
		return nil, err
	}

	id, err := uuid.Parse(req.Id)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid enrollment id")
	}

	e, err := h.enroll.Approve(ctx, admin, id)
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	return mapper.EnrollmentToProto(e), nil
}

func (h Handler) CancelEnrollment(ctx context.Context, req *userv1.CancelEnrollmentRequest) (*userv1.Enrollment, error) {
	admin, err := callerAccountID(ctx)
	if err != nil {
		return nil, err
	}

	id, err := uuid.Parse(req.Id)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid enrollment id")
	}

	e, err := h.enroll.Cancel(ctx, admin, id)
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	return mapper.EnrollmentToProto(e), nil
}

func (h Handler) ListEnrollments(ctx context.Context, req *userv1.ListEnrollmentsRequest) (*userv1.ListEnrollmentsResponse, error) {
	var filter enrollment.Filter
	if req.Status != nil {
		filter.Status = mapper.EnrollmentStatusFromProto(*req.Status)
	}

	list, err := h.enroll.List(ctx, filter)
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	out := make([]*userv1.Enrollment, 0, len(list))
	for _, e := range list {
		out = append(out, mapper.EnrollmentToProto(e))
	}

	return &userv1.ListEnrollmentsResponse{Enrollments: out}, nil
}
