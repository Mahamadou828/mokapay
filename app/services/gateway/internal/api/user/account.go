package user

import (
	"context"
	"net/http"

	"mokapay.app/app/services/gateway/internal/sdk/errs"
	"mokapay.app/app/services/gateway/internal/sdk/protocodec"
	coreerrs "mokapay.app/core/error"
	core "mokapay.app/core/http"
	userv1 "mokapay.app/proto/codebase/user/v1"
)

func (h Handler) me(ctx context.Context, _ *http.Request) core.Encoder {
	res, err := h.moka.User.User.Me(ctx, &userv1.MeRequest{})
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

func (h Handler) updateMe(ctx context.Context, r *http.Request) core.Encoder {
	req := &userv1.UpdateAccountRequest{}
	if err := core.Decode(r, protocodec.Proto{Message: req}); err != nil {
		return coreerrs.New(errs.InvalidArgument, err)
	}

	res, err := h.moka.User.User.UpdateAccount(ctx, req)
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}
