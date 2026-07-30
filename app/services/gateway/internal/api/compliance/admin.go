package compliance

import (
	"context"
	"net/http"

	"mokapay.app/app/services/gateway/internal/api/param"
	"mokapay.app/app/services/gateway/internal/sdk/errs"
	"mokapay.app/app/services/gateway/internal/sdk/protocodec"
	coreerrs "mokapay.app/core/error"
	core "mokapay.app/core/http"
	compliancev1 "mokapay.app/proto/codebase/compliance/v1"
)

// --- alerts ------------------------------------------------------------------

func (h Handler) listAlerts(ctx context.Context, r *http.Request) core.Encoder {
	status, err := param.Enum[compliancev1.AlertStatus](r, "status", compliancev1.AlertStatus_value)
	if err != nil {
		return coreerrs.New(errs.InvalidArgument, err)
	}
	severity, err := param.Enum[compliancev1.Severity](r, "severity", compliancev1.Severity_value)
	if err != nil {
		return coreerrs.New(errs.InvalidArgument, err)
	}
	limit, err := param.Int32(r, "limit")
	if err != nil {
		return coreerrs.New(errs.InvalidArgument, err)
	}

	req := &compliancev1.ListAlertsRequest{
		Status:   status,
		Severity: severity,
		Limit:    limit,
	}
	if id := r.URL.Query().Get("account_id"); id != "" {
		req.AccountId = &id
	}

	res, err := h.moka.Compliance.Admin.ListAlerts(ctx, req)
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

func (h Handler) getAlert(ctx context.Context, r *http.Request) core.Encoder {
	res, err := h.moka.Compliance.Admin.GetAlert(ctx, &compliancev1.GetAlertRequest{
		Id: core.Param(r, "id"),
	})
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

func (h Handler) updateAlertStatus(ctx context.Context, r *http.Request) core.Encoder {
	req := &compliancev1.UpdateAlertStatusRequest{}
	if err := core.Decode(r, protocodec.Proto{Message: req}); err != nil {
		return coreerrs.New(errs.InvalidArgument, err)
	}
	req.Id = core.Param(r, "id")

	res, err := h.moka.Compliance.Admin.UpdateAlertStatus(ctx, req)
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

// --- cases -------------------------------------------------------------------

func (h Handler) listCases(ctx context.Context, r *http.Request) core.Encoder {
	status, err := param.Enum[compliancev1.CaseStatus](r, "status", compliancev1.CaseStatus_value)
	if err != nil {
		return coreerrs.New(errs.InvalidArgument, err)
	}
	limit, err := param.Int32(r, "limit")
	if err != nil {
		return coreerrs.New(errs.InvalidArgument, err)
	}

	res, err := h.moka.Compliance.Admin.ListCases(ctx, &compliancev1.ListCasesRequest{
		Status: status,
		Limit:  limit,
	})
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

func (h Handler) createCase(ctx context.Context, r *http.Request) core.Encoder {
	req := &compliancev1.CreateCaseRequest{}
	if err := core.Decode(r, protocodec.Proto{Message: req}); err != nil {
		return coreerrs.New(errs.InvalidArgument, err)
	}

	res, err := h.moka.Compliance.Admin.CreateCase(ctx, req)
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

func (h Handler) updateCase(ctx context.Context, r *http.Request) core.Encoder {
	req := &compliancev1.UpdateCaseRequest{}
	if err := core.Decode(r, protocodec.Proto{Message: req}); err != nil {
		return coreerrs.New(errs.InvalidArgument, err)
	}
	req.Id = core.Param(r, "id")

	res, err := h.moka.Compliance.Admin.UpdateCase(ctx, req)
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

func (h Handler) addAlertToCase(ctx context.Context, r *http.Request) core.Encoder {
	req := &compliancev1.AddAlertToCaseRequest{}
	if err := core.Decode(r, protocodec.Proto{Message: req}); err != nil {
		return coreerrs.New(errs.InvalidArgument, err)
	}
	req.CaseId = core.Param(r, "id")

	res, err := h.moka.Compliance.Admin.AddAlertToCase(ctx, req)
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

// --- risk & dashboard --------------------------------------------------------

func (h Handler) getAccountRisk(ctx context.Context, r *http.Request) core.Encoder {
	res, err := h.moka.Compliance.Admin.GetAccountRisk(ctx, &compliancev1.GetAccountRiskRequest{
		AccountId: core.Param(r, "id"),
	})
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

func (h Handler) getDashboard(ctx context.Context, r *http.Request) core.Encoder {
	res, err := h.moka.Compliance.Admin.GetDashboard(ctx, &compliancev1.GetDashboardRequest{})
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}
