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

func (h Handler) listAllTransfers(ctx context.Context, r *http.Request) core.Encoder {
	status, err := param.Enum[paymentv1.TransferStatus](r, "status", paymentv1.TransferStatus_value)
	if err != nil {
		return coreerrs.New(errs.InvalidArgument, err)
	}
	limit, err := param.Int32(r, "limit")
	if err != nil {
		return coreerrs.New(errs.InvalidArgument, err)
	}

	res, err := h.moka.Payment.Admin.ListAllTransfers(ctx, &paymentv1.ListAllTransfersRequest{
		Status: status,
		Limit:  limit,
	})
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

func (h Handler) cancelTransferAdmin(ctx context.Context, r *http.Request) core.Encoder {
	req := &paymentv1.CancelTransferAdminRequest{}
	if err := core.Decode(r, protocodec.Proto{Message: req}); err != nil {
		return coreerrs.New(errs.InvalidArgument, err)
	}
	req.Id = core.Param(r, "id")

	res, err := h.moka.Payment.Admin.CancelTransferAdmin(ctx, req)
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}
