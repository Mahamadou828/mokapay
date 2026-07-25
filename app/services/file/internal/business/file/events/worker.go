package events

import (
	"context"
	"errors"

	"github.com/riverqueue/river"
	"mokapay.app/app/services/file/internal/business/file"
	"mokapay.app/core/client"
	"mokapay.app/core/logger"
	"mokapay.app/core/passport"
	paymentv1 "mokapay.app/proto/codebase/payment/v1"
)

type UploadArg struct {
	File file.File `json:"file"`
}

func (a UploadArg) Kind() string {
	return "file.upload"
}

type UploadWorker struct {
	river.WorkerDefaults[UploadArg]
	Moka   client.MokaClient
	Logger *logger.Logger
}

func (w *UploadWorker) Work(ctx context.Context, job *river.Job[UploadArg]) error {
	pass := passport.SystemPassport()
	ctx = passport.AddToContext(ctx, pass)

	data := job.Args.File
	if data.RelatedType != nil {
		switch *data.RelatedType {
		case "ledger_request":
			return w.handleLedgerRequest(ctx, data)
		}
	}

	return nil
}

func (w *UploadWorker) handleLedgerRequest(ctx context.Context, data file.File) error {
	// currently i can think of only one situation where i file can be uploaded for a ledger request
	// and it is only for a agent proof of deposit
	if data.Purpose != file.PurposeDepositProof {
		return errors.New("invalid purpose for ledger request")
	}

	// move ledger request to admin approval
	if _, err := w.Moka.Payment.Ledger.UpdateLedgerRequestStatus(ctx, &paymentv1.UpdateLedgerRequestStatusRequest{}); err != nil {
		w.Logger.Error(ctx, "File.Events: failed to update ledger request status", err)
		return err
	}

	return nil
}
