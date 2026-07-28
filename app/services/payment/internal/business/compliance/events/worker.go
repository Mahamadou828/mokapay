// Package events owns the compliance River topology: the evaluation job
// enqueued in the same transaction as the transfer completion that earns
// it, and the worker that ships the snapshot to the compliance service.
//
// The job is fire-and-forget by construction. It runs after the commit,
// its result is logged and never read back into the transfer flow, and it
// gives up rather than retrying forever: compliance detects, it never
// blocks money movement.
package events

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"mokapay.app/app/services/payment/internal/business/compliance"
	complianceuc "mokapay.app/app/services/payment/internal/business/compliance/usecase"
	"mokapay.app/core/client"
	"mokapay.app/core/logger"
	"mokapay.app/core/passport"
	compliancev1 "mokapay.app/proto/codebase/compliance/v1"
)

// maxAttempts bounds the retries. An evaluation that keeps failing is a
// lost audit row, which is bad — but a job retrying forever against a
// broken downstream is worse, and the failure is logged either way.
const maxAttempts = 3

// EvaluateArg runs the AML rule set for one completed transfer.
type EvaluateArg struct {
	TransferID string
}

func (EvaluateArg) Kind() string { return "compliance.evaluate_transfer" }

// EvaluateWorker computes the metrics snapshot from payment's own data and
// hands it to the compliance service under a system passport (the RPC is
// internal — it has no gateway route at all).
type EvaluateWorker struct {
	river.WorkerDefaults[EvaluateArg]
	UC     complianceuc.UseCase
	Moka   *client.MokaClient
	Logger *logger.Logger
}

func (w *EvaluateWorker) Work(ctx context.Context, job *river.Job[EvaluateArg]) error {
	id, err := uuid.Parse(job.Args.TransferID)
	if err != nil {
		return nil // malformed job — retrying can't help
	}
	if job.Attempt >= maxAttempts {
		w.Logger.Error(ctx, "Compliance.EvaluateWorker", "giving up on transfer evaluation", "transfer_id", id)
		return nil
	}

	snap, err := w.UC.Snapshot(ctx, id)
	if err != nil {
		w.Logger.Error(ctx, "Compliance.EvaluateWorker", "building metrics snapshot failed", "transfer_id", id, "error", err)
		if errors.Is(err, compliance.ErrAccountNotFound) {
			return nil // permanent: the subject account will not appear
		}
		return err
	}

	ctx = passport.AddToContext(ctx, passport.SystemPassport())

	res, err := w.Moka.Compliance.Compliance.EvaluateTransfer(ctx, snapshotToProto(snap))
	if err != nil {
		w.Logger.Error(ctx, "Compliance.EvaluateWorker", "evaluation call failed", "transfer_id", id, "error", err)
		return err
	}

	w.Logger.Info(ctx, "Compliance.EvaluateWorker", "transfer evaluated",
		"transfer_id", id,
		"rules", len(res.GetEvaluations()),
		"triggered", res.GetTriggeredCount())

	return nil
}

// snapshotToProto maps the snapshot onto the contract. Amounts are already
// whole USD here — the conversion from minor units happened once, in the
// business package.
func snapshotToProto(in compliance.Snapshot) *compliancev1.EvaluateTransferRequest {
	out := &compliancev1.EvaluateTransferRequest{
		TransferId: in.TransferID.String(),
		Sender:     accountMetricsToProto(in.Sender),
		Recipient: &compliancev1.RecipientMetrics{
			Phone:              in.Recipient.Phone,
			DistinctSenders_7D: in.Recipient.DistinctSenders7d,
		},
	}
	if in.Agent != nil {
		out.Agent = accountMetricsToProto(*in.Agent)
	}

	return out
}

func accountMetricsToProto(in compliance.AccountMetrics) *compliancev1.TransferMetrics {
	return &compliancev1.TransferMetrics{
		AccountId:                    in.AccountID.String(),
		CurrentAmountUsd:             in.CurrentAmountUSD,
		Rolling_24HVolumeUsd:         in.Rolling24hVolumeUSD,
		Rolling_7DVolumeUsd:          in.Rolling7dVolumeUSD,
		Rolling_7DCount:              in.Rolling7dCount,
		Rolling_30DAvgDailyVolumeUsd: in.Rolling30dAvgDailyVolumeUSD,
		Rolling_1HCount:              in.Rolling1hCount,
		Rolling_7DNighttimeCount:     in.Rolling7dNighttimeCount,
		AccountAgeDays:               in.AccountAgeDays,
	}
}
