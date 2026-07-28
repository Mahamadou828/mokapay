package events

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"mokapay.app/app/services/payment/internal/business/matching"
	matchinguc "mokapay.app/app/services/payment/internal/business/matching/usecase"
	transferrepo "mokapay.app/app/services/payment/internal/business/transfer/repo"
	transferuc "mokapay.app/app/services/payment/internal/business/transfer/usecase"
	"mokapay.app/core/client"
	"mokapay.app/core/logger"
	"mokapay.app/core/passport"
	userv1 "mokapay.app/proto/codebase/user/v1"
)

// MatchWorker runs one broadcast round. An empty candidate pool snoozes
// the job (the deadline check inside RunMatch eventually expires the
// transfer), so a market with no agents right now never drops a
// transfer on the floor.
type MatchWorker struct {
	river.WorkerDefaults[MatchArg]
	UC     matchinguc.UseCase
	Logger *logger.Logger
}

func (w *MatchWorker) Work(ctx context.Context, job *river.Job[MatchArg]) error {
	id, err := uuid.Parse(job.Args.TransferID)
	if err != nil {
		return nil // malformed job — retrying can't help
	}
	if err := w.UC.RunMatch(ctx, id); err != nil {
		if errors.Is(err, matching.ErrNoCandidates) {
			return river.JobSnooze(30 * time.Second)
		}
		w.Logger.Error(ctx, "Transfer.MatchWorker", "match round failed", err)
		return err
	}
	return nil
}

// OffersExpireWorker fires at the batch's expires_at.
type OffersExpireWorker struct {
	river.WorkerDefaults[OffersExpireArg]
	UC     matchinguc.UseCase
	Logger *logger.Logger
}

func (w *OffersExpireWorker) Work(ctx context.Context, job *river.Job[OffersExpireArg]) error {
	id, err := uuid.Parse(job.Args.TransferID)
	if err != nil {
		return nil
	}
	if err := w.UC.HandleOffersExpired(ctx, id); err != nil {
		w.Logger.Error(ctx, "Transfer.OffersExpireWorker", "expiry failed", err)
		return err
	}
	return nil
}

// PaymentTimeoutWorker fires when the client's payment window elapses;
// stale clocks (the proof arrived first) no-op inside the usecase.
type PaymentTimeoutWorker struct {
	river.WorkerDefaults[PaymentTimeoutArg]
	UC     transferuc.UseCase
	Logger *logger.Logger
}

func (w *PaymentTimeoutWorker) Work(ctx context.Context, job *river.Job[PaymentTimeoutArg]) error {
	id, err := uuid.Parse(job.Args.TransferID)
	if err != nil {
		return nil
	}
	return w.UC.HandlePaymentTimeout(ctx, id)
}

// AutoConfirmWorker completes on the client's behalf after the
// confirmation window (D11).
type AutoConfirmWorker struct {
	river.WorkerDefaults[AutoConfirmArg]
	UC     transferuc.UseCase
	Logger *logger.Logger
}

func (w *AutoConfirmWorker) Work(ctx context.Context, job *river.Job[AutoConfirmArg]) error {
	id, err := uuid.Parse(job.Args.TransferID)
	if err != nil {
		return nil
	}
	return w.UC.HandleAutoConfirm(ctx, id)
}

// NotifyWorker pushes through the user service under the system
// passport (same shape as the ledger notifier).
type NotifyWorker struct {
	river.WorkerDefaults[NotifyArg]
	Moka   *client.MokaClient
	Logger *logger.Logger
}

func (w *NotifyWorker) Work(ctx context.Context, job *river.Job[NotifyArg]) error {
	// drop notification after 3 attempt
	if job.Attempt >= 3 {
		return nil
	}

	ctx = passport.AddToContext(ctx, passport.SystemPassport())

	if _, err := w.Moka.User.Notification.PushNotification(ctx, &userv1.PushNotificationRequest{
		AccountId: job.Args.To,
		EventType: job.Args.EventType,
		Title:     job.Args.Title,
		Body:      job.Args.Message,
	}); err != nil {
		w.Logger.Error(ctx, "Transfer.NotifyWorker", "push notification error", err)
		return err
	}
	return nil
}

// SweepArg is the periodic rescue sweep: re-enqueue pending_match
// transfers whose match job died somewhere. Belt and braces only — the
// transactional enqueue is the primary path.
type SweepArg struct{}

func (SweepArg) Kind() string { return "transfer.rescue_sweep" }

type SweepWorker struct {
	river.WorkerDefaults[SweepArg]
	Transfers transferrepo.Store
	River     *river.Client[pgx.Tx]
	Logger    *logger.Logger
}

func (w *SweepWorker) Work(ctx context.Context, _ *river.Job[SweepArg]) error {
	stale, err := w.Transfers.ListStalePendingMatch(ctx, time.Now().Add(-5*time.Minute), 100)
	if err != nil {
		return err
	}
	for _, id := range stale {
		if _, err := w.River.Insert(ctx, MatchArg{TransferID: id.String()}, nil); err != nil {
			w.Logger.Error(ctx, "Transfer.SweepWorker", "re-enqueue failed", err)
			return err
		}
	}
	if len(stale) > 0 {
		w.Logger.Info(ctx, "Transfer.SweepWorker", "requeued stale transfers", len(stale))
	}
	return nil
}
