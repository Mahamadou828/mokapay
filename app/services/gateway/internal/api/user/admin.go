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

func (h Handler) listAccounts(ctx context.Context, r *http.Request) core.Encoder {
	role, err := param.Enum[userv1.AccountRole](r, "role", userv1.AccountRole_value)
	if err != nil {
		return coreerrs.New(errs.InvalidArgument, err)
	}
	status, err := param.Enum[userv1.AccountStatus](r, "status", userv1.AccountStatus_value)
	if err != nil {
		return coreerrs.New(errs.InvalidArgument, err)
	}

	req := &userv1.ListAccountsRequest{Role: role, Status: status}
	if phone := r.URL.Query().Get("phone_number"); phone != "" {
		req.PhoneNumber = &phone
	}

	res, err := h.moka.User.Admin.ListAccounts(ctx, req)
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

func (h Handler) getAccount(ctx context.Context, r *http.Request) core.Encoder {
	res, err := h.moka.User.Admin.GetAccount(ctx, &userv1.GetAccountRequest{Id: core.Param(r, "id")})
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

// createAccount provisions a back-office (ops/admin) account. The route
// sits on the ops tier, but the user service enforces the narrower gate:
// only an admin caller passes.
func (h Handler) createAccount(ctx context.Context, r *http.Request) core.Encoder {
	req := &userv1.AdminServiceCreateAccountRequest{}
	if err := core.Decode(r, protocodec.Proto{Message: req}); err != nil {
		return coreerrs.New(errs.InvalidArgument, err)
	}

	res, err := h.moka.User.Admin.CreateAccount(ctx, req)
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

func (h Handler) updateAccount(ctx context.Context, r *http.Request) core.Encoder {
	req := &userv1.AdminServiceUpdateAccountRequest{}
	if err := core.Decode(r, protocodec.Proto{Message: req}); err != nil {
		return coreerrs.New(errs.InvalidArgument, err)
	}
	req.Id = core.Param(r, "id") // the path owns the id, never the body

	res, err := h.moka.User.Admin.UpdateAccount(ctx, req)
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

func (h Handler) deleteAccount(ctx context.Context, r *http.Request) core.Encoder {
	if _, err := h.moka.User.Admin.DeleteAccount(ctx, &userv1.DeleteAccountRequest{Id: core.Param(r, "id")}); err != nil {
		return rpcErr(err)
	}

	return nil // 204
}
