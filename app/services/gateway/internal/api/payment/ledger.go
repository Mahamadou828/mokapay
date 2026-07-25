package payment

import (
	"context"
	"errors"
	"net/http"

	"mokapay.app/app/services/gateway/internal/api/param"
	"mokapay.app/app/services/gateway/internal/sdk/errs"
	"mokapay.app/app/services/gateway/internal/sdk/protocodec"
	coreerrs "mokapay.app/core/error"
	core "mokapay.app/core/http"
	paymentv1 "mokapay.app/proto/codebase/payment/v1"
)

// --- agent side (scope pinned service-side to the caller's profile) ---------

func (h Handler) createMyLedgerRequest(ctx context.Context, r *http.Request) core.Encoder {
	req := &paymentv1.CreateLedgerRequestRequest{}
	if err := core.Decode(r, protocodec.Proto{Message: req}); err != nil {
		return coreerrs.New(errs.InvalidArgument, err)
	}
	// The engine resolves the agent's own profile from the passport; a
	// claimed profile id is ignored anyway — drop it for clarity.
	req.AgentProfileId = ""

	res, err := h.moka.Payment.Ledger.CreateLedgerRequest(ctx, req)
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

func (h Handler) listMyLedgerRequests(ctx context.Context, r *http.Request) core.Encoder {
	req, enc := ledgerListFilters(r)
	if enc != nil {
		return enc
	}

	res, err := h.moka.Payment.Ledger.ListLedgerRequests(ctx, req)
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

func (h Handler) listMyLedgerLines(ctx context.Context, r *http.Request) core.Encoder {
	limit, err := param.Int32(r, "limit")
	if err != nil {
		return coreerrs.New(errs.InvalidArgument, err)
	}

	// No profile id: the service pins the statement to the caller's own.
	res, err := h.moka.Payment.Ledger.ListLedgerLines(ctx, &paymentv1.ListLedgerLinesRequest{
		Limit: limit,
	})
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

// --- ops side ----------------------------------------------------------------

func (h Handler) createLedgerRequestAdmin(ctx context.Context, r *http.Request) core.Encoder {
	req := &paymentv1.CreateLedgerRequestRequest{}
	if err := core.Decode(r, protocodec.Proto{Message: req}); err != nil {
		return coreerrs.New(errs.InvalidArgument, err)
	}

	res, err := h.moka.Payment.Ledger.CreateLedgerRequest(ctx, req)
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

func (h Handler) listLedgerRequestsAdmin(ctx context.Context, r *http.Request) core.Encoder {
	req, enc := ledgerListFilters(r)
	if enc != nil {
		return enc
	}
	if id := r.URL.Query().Get("agent_profile_id"); id != "" {
		req.AgentProfileId = &id
	}

	res, err := h.moka.Payment.Ledger.ListLedgerRequests(ctx, req)
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

func (h Handler) getLedgerRequest(ctx context.Context, r *http.Request) core.Encoder {
	res, err := h.moka.Payment.Ledger.GetLedgerRequest(ctx, &paymentv1.GetLedgerRequestRequest{
		Id: core.Param(r, "id"),
	})
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

func (h Handler) approveLedgerRequest(ctx context.Context, r *http.Request) core.Encoder {
	res, err := h.moka.Payment.Ledger.ApproveLedgerRequest(ctx, &paymentv1.ApproveLedgerRequestRequest{
		Id: core.Param(r, "id"),
	})
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

func (h Handler) rejectLedgerRequest(ctx context.Context, r *http.Request) core.Encoder {
	req := &paymentv1.RejectLedgerRequestRequest{}
	if err := core.Decode(r, protocodec.Proto{Message: req}); err != nil {
		return coreerrs.New(errs.InvalidArgument, err)
	}
	req.Id = core.Param(r, "id")

	res, err := h.moka.Payment.Ledger.RejectLedgerRequest(ctx, req)
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

func (h Handler) listLedgerLinesAdmin(ctx context.Context, r *http.Request) core.Encoder {
	limit, err := param.Int32(r, "limit")
	if err != nil {
		return coreerrs.New(errs.InvalidArgument, err)
	}

	res, err := h.moka.Payment.Ledger.ListLedgerLines(ctx, &paymentv1.ListLedgerLinesRequest{
		AgentProfileId: r.URL.Query().Get("agent_profile_id"),
		Limit:          limit,
	})
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

func (h Handler) getAgentProfileAdmin(ctx context.Context, r *http.Request) core.Encoder {
	res, err := h.moka.Payment.Admin.GetAgentProfile(ctx, &paymentv1.GetAgentProfileRequest{
		Selector: &paymentv1.GetAgentProfileRequest_Id{Id: core.Param(r, "id")},
	})
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

func (h Handler) getAgentProfileByAccount(ctx context.Context, r *http.Request) core.Encoder {
	accountID := r.URL.Query().Get("account_id")
	if accountID == "" {
		return coreerrs.New(errs.InvalidArgument, errors.New("account_id is required"))
	}

	res, err := h.moka.Payment.Admin.GetAgentProfile(ctx, &paymentv1.GetAgentProfileRequest{
		Selector: &paymentv1.GetAgentProfileRequest_AccountId{AccountId: accountID},
	})
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

func (h Handler) activateAgentProfile(ctx context.Context, r *http.Request) core.Encoder {
	res, err := h.moka.Payment.Admin.ActivateAgentProfile(ctx, &paymentv1.ActivateAgentProfileRequest{
		Id: core.Param(r, "id"),
	})
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

func (h Handler) suspendAgentProfile(ctx context.Context, r *http.Request) core.Encoder {
	res, err := h.moka.Payment.Admin.SuspendAgentProfile(ctx, &paymentv1.SuspendAgentProfileRequest{
		Id: core.Param(r, "id"),
	})
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

// ledgerListFilters reads the shared status/type query filters.
func ledgerListFilters(r *http.Request) (*paymentv1.ListLedgerRequestsRequest, core.Encoder) {
	status, err := param.Enum[paymentv1.LedgerRequestStatus](r, "status", paymentv1.LedgerRequestStatus_value)
	if err != nil {
		return nil, coreerrs.New(errs.InvalidArgument, err)
	}
	typ, err := param.Enum[paymentv1.LedgerRequestType](r, "type", paymentv1.LedgerRequestType_value)
	if err != nil {
		return nil, coreerrs.New(errs.InvalidArgument, err)
	}
	return &paymentv1.ListLedgerRequestsRequest{Status: status, Type: typ}, nil
}
