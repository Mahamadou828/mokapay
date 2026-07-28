package payment

import (
	"context"
	"net/http"

	"mokapay.app/app/services/gateway/internal/api/param"
	"mokapay.app/app/services/gateway/internal/sdk/errs"
	"mokapay.app/app/services/gateway/internal/sdk/protocodec"
	coreerrs "mokapay.app/core/error"
	core "mokapay.app/core/http"
	filev1 "mokapay.app/proto/codebase/file/v1"
	paymentv1 "mokapay.app/proto/codebase/payment/v1"
)

// The file routes ride in the payment handler package: the proofs they
// carry exist to advance transfers and ledger requests, and the moka
// client is already here. The upload flow is presign + confirm — the
// bytes go straight to object storage, never through the gateway.

func (h Handler) createUpload(ctx context.Context, r *http.Request) core.Encoder {
	req := &filev1.CreateUploadRequest{}
	if err := core.Decode(r, protocodec.Proto{Message: req}); err != nil {
		return coreerrs.New(errs.InvalidArgument, err)
	}

	res, err := h.moka.File.File.CreateUpload(ctx, req)
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

func (h Handler) confirmUpload(ctx context.Context, r *http.Request) core.Encoder {
	res, err := h.moka.File.File.ConfirmUpload(ctx, &filev1.ConfirmUploadRequest{
		Id: core.Param(r, "id"),
	})
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

func (h Handler) getFile(ctx context.Context, r *http.Request) core.Encoder {
	res, err := h.moka.File.File.GetFile(ctx, &filev1.GetFileRequest{
		Id: core.Param(r, "id"),
	})
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

func (h Handler) listFiles(ctx context.Context, r *http.Request) core.Encoder {
	purpose, err := param.Enum[filev1.FilePurpose](r, "purpose", filev1.FilePurpose_value)
	if err != nil {
		return coreerrs.New(errs.InvalidArgument, err)
	}

	res, err := h.moka.File.File.ListFiles(ctx, &filev1.ListFilesRequest{
		RelatedType: r.URL.Query().Get("related_type"),
		RelatedId:   r.URL.Query().Get("related_id"),
		Purpose:     purpose,
	})
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

// listTransferFiles goes through the payment service, not the file
// service: a transfer's proofs are readable by every party (client,
// assigned agent, ops), and only payment knows who the parties are.
func (h Handler) listTransferFiles(ctx context.Context, r *http.Request) core.Encoder {
	res, err := h.moka.Payment.Transfer.ListTransferFiles(ctx, &paymentv1.ListTransferFilesRequest{
		TransferId: core.Param(r, "id"),
	})
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

func (h Handler) rejectFile(ctx context.Context, r *http.Request) core.Encoder {
	res, err := h.moka.File.File.RejectFile(ctx, &filev1.RejectFileRequest{
		Id: core.Param(r, "id"),
	})
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}
