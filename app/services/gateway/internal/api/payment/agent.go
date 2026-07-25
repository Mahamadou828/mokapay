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

func (h Handler) getMyProfile(ctx context.Context, _ *http.Request) core.Encoder {
	res, err := h.moka.Payment.Agent.GetMyProfile(ctx, &paymentv1.GetMyProfileRequest{})
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

func (h Handler) listMyOffers(ctx context.Context, r *http.Request) core.Encoder {
	status, err := param.Enum[paymentv1.OfferStatus](r, "status", paymentv1.OfferStatus_value)
	if err != nil {
		return coreerrs.New(errs.InvalidArgument, err)
	}
	limit, err := param.Int32(r, "limit")
	if err != nil {
		return coreerrs.New(errs.InvalidArgument, err)
	}

	res, err := h.moka.Payment.Agent.ListMyOffers(ctx, &paymentv1.ListMyOffersRequest{
		Status: status,
		Limit:  limit,
	})
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

func (h Handler) acceptOffer(ctx context.Context, r *http.Request) core.Encoder {
	res, err := h.moka.Payment.Agent.AcceptOffer(ctx, &paymentv1.AcceptOfferRequest{
		OfferId: core.Param(r, "id"),
	})
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

func (h Handler) declineOffer(ctx context.Context, r *http.Request) core.Encoder {
	res, err := h.moka.Payment.Agent.DeclineOffer(ctx, &paymentv1.DeclineOfferRequest{
		OfferId: core.Param(r, "id"),
	})
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

func (h Handler) listMyTransfers(ctx context.Context, r *http.Request) core.Encoder {
	status, err := param.Enum[paymentv1.TransferStatus](r, "status", paymentv1.TransferStatus_value)
	if err != nil {
		return coreerrs.New(errs.InvalidArgument, err)
	}
	limit, err := param.Int32(r, "limit")
	if err != nil {
		return coreerrs.New(errs.InvalidArgument, err)
	}

	res, err := h.moka.Payment.Agent.ListMyTransfers(ctx, &paymentv1.ListMyTransfersRequest{
		Status: status,
		Limit:  limit,
	})
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

func (h Handler) listMyWallets(ctx context.Context, _ *http.Request) core.Encoder {
	res, err := h.moka.Payment.Agent.ListMyWallets(ctx, &paymentv1.ListMyWalletsRequest{})
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

func (h Handler) addWallet(ctx context.Context, r *http.Request) core.Encoder {
	req := &paymentv1.AddWalletRequest{}
	if err := core.Decode(r, protocodec.Proto{Message: req}); err != nil {
		return coreerrs.New(errs.InvalidArgument, err)
	}

	res, err := h.moka.Payment.Agent.AddWallet(ctx, req)
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

func (h Handler) removeWallet(ctx context.Context, r *http.Request) core.Encoder {
	res, err := h.moka.Payment.Agent.RemoveWallet(ctx, &paymentv1.RemoveWalletRequest{
		Id: core.Param(r, "id"),
	})
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}
