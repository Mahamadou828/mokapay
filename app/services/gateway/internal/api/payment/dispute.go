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

// openDispute is the client/agent tier: either party of a post-accept
// transfer can freeze it. The payment service derives which party the
// caller is from the passport.
func (h Handler) openDispute(ctx context.Context, r *http.Request) core.Encoder {
	req := &paymentv1.OpenDisputeRequest{}
	if err := core.Decode(r, protocodec.Proto{Message: req}); err != nil {
		return coreerrs.New(errs.InvalidArgument, err)
	}
	req.Id = core.Param(r, "id") // the path owns the id, never the body

	res, err := h.moka.Payment.Transfer.OpenDispute(ctx, req)
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

// listDisputes is the ops queue.
func (h Handler) listDisputes(ctx context.Context, r *http.Request) core.Encoder {
	status, err := param.Enum[paymentv1.DisputeStatus](r, "status", paymentv1.DisputeStatus_value)
	if err != nil {
		return coreerrs.New(errs.InvalidArgument, err)
	}
	limit, err := param.Int32(r, "limit")
	if err != nil {
		return coreerrs.New(errs.InvalidArgument, err)
	}

	req := &paymentv1.ListDisputesRequest{Status: status, Limit: limit}
	if id := r.URL.Query().Get("transfer_id"); id != "" {
		req.TransferId = &id
	}
	if id := r.URL.Query().Get("agent_profile_id"); id != "" {
		req.AgentProfileId = &id
	}

	res, err := h.moka.Payment.Admin.ListDisputes(ctx, req)
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

// resolveDispute is the ops ruling — it releases or captures an agent's
// guarantee, so the payment service demands an accountable human.
func (h Handler) resolveDispute(ctx context.Context, r *http.Request) core.Encoder {
	req := &paymentv1.ResolveDisputeRequest{}
	if err := core.Decode(r, protocodec.Proto{Message: req}); err != nil {
		return coreerrs.New(errs.InvalidArgument, err)
	}
	req.Id = core.Param(r, "id")

	res, err := h.moka.Payment.Admin.ResolveDispute(ctx, req)
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}
