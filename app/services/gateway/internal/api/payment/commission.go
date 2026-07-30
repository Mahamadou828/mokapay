package payment

import (
	"context"
	"net/http"

	"mokapay.app/app/services/gateway/internal/api/param"
	"mokapay.app/app/services/gateway/internal/sdk/errs"
	"mokapay.app/app/services/gateway/internal/sdk/protocodec"
	coreerrs "mokapay.app/core/error"
	core "mokapay.app/core/http"
	paymentv1 "mokapay.app/proto/codebase/payment/v1"
)

// listMyCommissions is the agent tier: the caller's own earnings, scoped
// by the payment service from the passport.
func (h Handler) listMyCommissions(ctx context.Context, r *http.Request) core.Encoder {
	status, err := param.Enum[paymentv1.CommissionStatus](r, "status", paymentv1.CommissionStatus_value)
	if err != nil {
		return coreerrs.New(errs.InvalidArgument, err)
	}
	limit, err := param.Int32(r, "limit")
	if err != nil {
		return coreerrs.New(errs.InvalidArgument, err)
	}

	res, err := h.moka.Payment.Agent.ListMyCommissions(ctx, &paymentv1.ListMyCommissionsRequest{
		Status: status,
		Limit:  limit,
	})
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

// listCommissionBatches is the ops payout view.
func (h Handler) listCommissionBatches(ctx context.Context, r *http.Request) core.Encoder {
	status, err := param.Enum[paymentv1.CommissionStatus](r, "status", paymentv1.CommissionStatus_value)
	if err != nil {
		return coreerrs.New(errs.InvalidArgument, err)
	}
	limit, err := param.Int32(r, "limit")
	if err != nil {
		return coreerrs.New(errs.InvalidArgument, err)
	}

	req := &paymentv1.ListCommissionBatchesRequest{Status: status, Limit: limit}
	if id := r.URL.Query().Get("agent_profile_id"); id != "" {
		req.AgentProfileId = &id
	}

	res, err := h.moka.Payment.Admin.ListCommissionBatches(ctx, req)
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}
