package events

import (
	"context"
	"errors"
	"fmt"

	"github.com/riverqueue/river"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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

// UploadWorker fans a stored file out to the service its purpose gates:
// proofs advance the payment service's workflows (ledger requests and
// transfers) under the system passport. The remote transitions are
// forward-only and status-guarded, so a redelivered job is idempotent —
// FailedPrecondition/NotFound answers mean "already applied or moved
// on" and complete the job instead of retrying it.
type UploadWorker struct {
	river.WorkerDefaults[UploadArg]
	Moka   client.MokaClient
	Logger *logger.Logger
}

func (w *UploadWorker) Work(ctx context.Context, job *river.Job[UploadArg]) error {
	ctx = passport.AddToContext(ctx, passport.SystemPassport())

	data := job.Args.File
	if data.RelatedType == nil || data.RelatedID == nil {
		return nil // unattached upload — nothing to advance
	}

	switch *data.RelatedType {
	case "ledger_request":
		return w.handleLedgerRequest(ctx, data)
	case "transfer":
		return w.handleTransfer(ctx, data)
	}

	return nil
}

// handleLedgerRequest: an agent's deposit proof forwards the request to
// admin approval.
func (w *UploadWorker) handleLedgerRequest(ctx context.Context, data file.File) error {
	if data.Purpose != file.PurposeDepositProof {
		return errors.New("invalid purpose for ledger request")
	}

	proofID := data.ID.String()
	if _, err := w.Moka.Payment.Ledger.UpdateLedgerRequestStatus(ctx, &paymentv1.UpdateLedgerRequestStatusRequest{
		Id:          data.RelatedID.String(),
		Status:      paymentv1.LedgerRequestStatus_LEDGER_REQUEST_STATUS_PENDING_ADMIN_APPROVAL,
		ProofFileId: &proofID,
	}); err != nil {
		return w.settle(ctx, "ledger request", err)
	}

	return nil
}

// handleTransfer: the two proof-gated moves — the client's payment
// proof hands the ball to the agent, the agent's payout proof opens the
// client confirmation window. A proof can never reach done.
func (w *UploadWorker) handleTransfer(ctx context.Context, data file.File) error {
	var target paymentv1.TransferStatus
	switch data.Purpose {
	case file.PurposeSenderProof:
		target = paymentv1.TransferStatus_TRANSFER_STATUS_PENDING_AGENT_TRANSFER
	case file.PurposeAgentProof:
		target = paymentv1.TransferStatus_TRANSFER_STATUS_PENDING_CLIENT_CONFIRMATION
	default:
		return fmt.Errorf("purpose %q cannot advance a transfer", data.Purpose)
	}

	if _, err := w.Moka.Payment.Transfer.UpdateTransferStatus(ctx, &paymentv1.UpdateTransferStatusRequest{
		Id:          data.RelatedID.String(),
		Status:      target,
		ProofFileId: data.ID.String(),
	}); err != nil {
		return w.settle(ctx, "transfer", err)
	}

	return nil
}

// settle decides a remote error's fate: guard rejections mean the move
// already happened (or the entity is gone) — the job completes; real
// failures retry.
func (w *UploadWorker) settle(ctx context.Context, entity string, err error) error {
	switch status.Code(err) {
	case codes.FailedPrecondition, codes.NotFound, codes.InvalidArgument:
		w.Logger.Info(ctx, "File.Events", "proof already applied or entity gone", entity)
		return nil
	}
	w.Logger.Error(ctx, "File.Events: failed to advance "+entity, "error", err)
	return err
}
