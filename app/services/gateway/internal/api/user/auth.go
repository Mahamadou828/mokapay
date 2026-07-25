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

func (h Handler) sendOtp(ctx context.Context, r *http.Request) core.Encoder {
	req := &userv1.SendOtpRequest{}
	if err := core.Decode(r, protocodec.Proto{Message: req}); err != nil {
		return coreerrs.New(errs.InvalidArgument, err)
	}

	res, err := h.moka.User.Auth.SendOtp(ctx, req)
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

func (h Handler) register(ctx context.Context, r *http.Request) core.Encoder {
	req := &userv1.RegisterRequest{}
	if err := core.Decode(r, protocodec.Proto{Message: req}); err != nil {
		return coreerrs.New(errs.InvalidArgument, err)
	}

	res, err := h.moka.User.Auth.Register(ctx, req)
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

func (h Handler) login(ctx context.Context, r *http.Request) core.Encoder {
	req := &userv1.LoginRequest{}
	if err := core.Decode(r, protocodec.Proto{Message: req}); err != nil {
		return coreerrs.New(errs.InvalidArgument, err)
	}

	res, err := h.moka.User.Auth.Login(ctx, req)
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

func (h Handler) refreshToken(ctx context.Context, r *http.Request) core.Encoder {
	req := &userv1.RefreshTokenRequest{}
	if err := core.Decode(r, protocodec.Proto{Message: req}); err != nil {
		return coreerrs.New(errs.InvalidArgument, err)
	}

	res, err := h.moka.User.Auth.RefreshToken(ctx, req)
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

func (h Handler) changePassword(ctx context.Context, r *http.Request) core.Encoder {
	req := &userv1.ChangePasswordRequest{}
	if err := core.Decode(r, protocodec.Proto{Message: req}); err != nil {
		return coreerrs.New(errs.InvalidArgument, err)
	}

	if _, err := h.moka.User.Auth.ChangePassword(ctx, req); err != nil {
		return rpcErr(err)
	}

	return nil // 204
}

func (h Handler) logout(ctx context.Context, _ *http.Request) core.Encoder {
	if _, err := h.moka.User.Auth.Logout(ctx, &userv1.LogoutRequest{}); err != nil {
		return rpcErr(err)
	}

	return nil // 204
}
