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

func (h Handler) getAvailableOperators(ctx context.Context, r *http.Request) core.Encoder {
	from, err := param.Enum[paymentv1.Currency](r, "from", paymentv1.Currency_value)
	if err != nil {
		return coreerrs.New(errs.InvalidArgument, err)
	}
	to, err := param.Enum[paymentv1.Currency](r, "to", paymentv1.Currency_value)
	if err != nil {
		return coreerrs.New(errs.InvalidArgument, err)
	}

	req := &paymentv1.GetAvailableOperatorsRequest{}
	if from != nil {
		req.FromCurrency = *from
	}
	if to != nil {
		req.ToCurrency = *to
	}

	res, err := h.moka.Payment.Transfer.GetAvailableOperators(ctx, req)
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

func (h Handler) getQuote(ctx context.Context, r *http.Request) core.Encoder {
	req := &paymentv1.GetQuoteRequest{}
	if err := core.Decode(r, protocodec.Proto{Message: req}); err != nil {
		return coreerrs.New(errs.InvalidArgument, err)
	}

	res, err := h.moka.Payment.Transfer.GetQuote(ctx, req)
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

func (h Handler) createTransfer(ctx context.Context, r *http.Request) core.Encoder {
	req := &paymentv1.CreateTransferRequest{}
	if err := core.Decode(r, protocodec.Proto{Message: req}); err != nil {
		return coreerrs.New(errs.InvalidArgument, err)
	}

	res, err := h.moka.Payment.Transfer.CreateTransfer(ctx, req)
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

func (h Handler) getTransfer(ctx context.Context, r *http.Request) core.Encoder {
	res, err := h.moka.Payment.Transfer.GetTransfer(ctx, &paymentv1.GetTransferRequest{
		Id: core.Param(r, "id"),
	})
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

func (h Handler) listTransfers(ctx context.Context, r *http.Request) core.Encoder {
	status, err := param.Enum[paymentv1.TransferStatus](r, "status", paymentv1.TransferStatus_value)
	if err != nil {
		return coreerrs.New(errs.InvalidArgument, err)
	}
	limit, err := param.Int32(r, "limit")
	if err != nil {
		return coreerrs.New(errs.InvalidArgument, err)
	}

	res, err := h.moka.Payment.Transfer.ListTransfers(ctx, &paymentv1.ListTransfersRequest{
		Status: status,
		Limit:  limit,
	})
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

func (h Handler) listTransferEvents(ctx context.Context, r *http.Request) core.Encoder {
	res, err := h.moka.Payment.Transfer.ListTransferEvents(ctx, &paymentv1.ListTransferEventsRequest{
		TransferId: core.Param(r, "id"),
	})
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

func (h Handler) confirmReceipt(ctx context.Context, r *http.Request) core.Encoder {
	res, err := h.moka.Payment.Transfer.ConfirmReceipt(ctx, &paymentv1.ConfirmReceiptRequest{
		Id: core.Param(r, "id"),
	})
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

func (h Handler) cancelTransfer(ctx context.Context, r *http.Request) core.Encoder {
	req := &paymentv1.CancelTransferRequest{}
	// The body only ever carries the optional reason; the path owns the id.
	if r.ContentLength > 0 {
		if err := core.Decode(r, protocodec.Proto{Message: req}); err != nil {
			return coreerrs.New(errs.InvalidArgument, err)
		}
	}
	req.Id = core.Param(r, "id")

	res, err := h.moka.Payment.Transfer.CancelTransfer(ctx, req)
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

func (h Handler) listBeneficiaries(ctx context.Context, _ *http.Request) core.Encoder {
	res, err := h.moka.Payment.Transfer.ListBeneficiaries(ctx, &paymentv1.ListBeneficiariesRequest{})
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

func (h Handler) deleteBeneficiary(ctx context.Context, r *http.Request) core.Encoder {
	res, err := h.moka.Payment.Transfer.DeleteBeneficiary(ctx, &paymentv1.DeleteBeneficiaryRequest{
		Id: core.Param(r, "id"),
	})
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}
