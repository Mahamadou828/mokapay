package user

import (
	"context"
	"net/http"

	"mokapay.app/app/services/gateway/internal/api/param"
	"mokapay.app/app/services/gateway/internal/sdk/errs"
	"mokapay.app/app/services/gateway/internal/sdk/protocodec"
	coreerrs "mokapay.app/core/error"
	core "mokapay.app/core/http"
	userv1 "mokapay.app/proto/codebase/user/v1"
)

func (h Handler) getMyEnrollment(ctx context.Context, _ *http.Request) core.Encoder {
	res, err := h.moka.User.User.GetMyEnrollment(ctx, &userv1.GetMyEnrollmentRequest{})
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

func (h Handler) acceptEnrollment(ctx context.Context, r *http.Request) core.Encoder {
	req := &userv1.AcceptEnrollmentRequest{}
	if err := core.Decode(r, protocodec.Proto{Message: req}); err != nil {
		return coreerrs.New(errs.InvalidArgument, err)
	}

	res, err := h.moka.User.User.AcceptEnrollment(ctx, req)
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

// selfEnroll is AG-04's submission — the one call that creates the
// enrollment, stamps the terms and opens the guarantee funding.
func (h Handler) selfEnroll(ctx context.Context, r *http.Request) core.Encoder {
	req := &userv1.SelfEnrollRequest{}
	if err := core.Decode(r, protocodec.Proto{Message: req}); err != nil {
		return coreerrs.New(errs.InvalidArgument, err)
	}

	res, err := h.moka.User.User.SelfEnroll(ctx, req)
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

func (h Handler) declineEnrollment(ctx context.Context, r *http.Request) core.Encoder {
	req := &userv1.DeclineEnrollmentRequest{}
	if err := core.Decode(r, protocodec.Proto{Message: req}); err != nil {
		return coreerrs.New(errs.InvalidArgument, err)
	}

	res, err := h.moka.User.User.DeclineEnrollment(ctx, req)
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

func (h Handler) listEnrollments(ctx context.Context, r *http.Request) core.Encoder {
	status, err := param.Enum[userv1.EnrollmentStatus](r, "status", userv1.EnrollmentStatus_value)
	if err != nil {
		return coreerrs.New(errs.InvalidArgument, err)
	}

	res, err := h.moka.User.Admin.ListEnrollments(ctx, &userv1.ListEnrollmentsRequest{Status: status})
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

func (h Handler) promoteUserToAgent(ctx context.Context, r *http.Request) core.Encoder {
	res, err := h.moka.User.Admin.PromoteUserToAgent(ctx, &userv1.PromoteUserToAgentRequest{
		AccountId: core.Param(r, "id"), // the path owns the account id
	})
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

func (h Handler) approveEnrollment(ctx context.Context, r *http.Request) core.Encoder {
	res, err := h.moka.User.Admin.ApproveEnrollment(ctx, &userv1.ApproveEnrollmentRequest{
		Id: core.Param(r, "id"),
	})
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

func (h Handler) cancelEnrollment(ctx context.Context, r *http.Request) core.Encoder {
	res, err := h.moka.User.Admin.CancelEnrollment(ctx, &userv1.CancelEnrollmentRequest{
		Id: core.Param(r, "id"),
	})
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}
