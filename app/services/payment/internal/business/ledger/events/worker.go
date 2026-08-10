package events

import (
	"context"

	"github.com/riverqueue/river"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"mokapay.app/core/client"
	"mokapay.app/core/logger"
	"mokapay.app/core/passport"
	userv1 "mokapay.app/proto/codebase/user/v1"
)

type LedgerNotifyArg struct {
	To        string
	Title     string
	Message   string
	EventType string
}

func (a LedgerNotifyArg) Kind() string {
	return "ledger.notification"
}

type LedgerNotifyWorker struct {
	river.WorkerDefaults[LedgerNotifyArg]
	Moka   *client.MokaClient
	Logger *logger.Logger
}

func (w *LedgerNotifyWorker) Work(ctx context.Context, job *river.Job[LedgerNotifyArg]) error {
	// drop notification after 3 attempt
	if job.Attempt >= 3 {
		return nil
	}
	pass := passport.SystemPassport()
	ctx = passport.AddToContext(ctx, pass)

	if _, err := w.Moka.User.Notification.PushNotification(ctx, &userv1.PushNotificationRequest{
		AccountId: job.Args.To,
		EventType: job.Args.EventType,
		Title:     job.Args.Title,
		Body:      job.Args.Message,
	}); err != nil {
		w.Logger.Error(ctx, "Ledger.Notify", "push notification error", err)
		return err
	}
	return nil
}

// maxSettleAttempts bounds the retries on the enrollment hand-off. Giving
// up is not silent data loss: the enrollment stays accepted with a settled
// deposit, and ops can still finish it with the ordinary approve action.
const maxSettleAttempts = 5

// SettleEnrollmentArg tells the user service that the guarantee deposit
// backing this enrollment is settled. Deliberately carries ids only —
// the user service owns every rule about what that means.
type SettleEnrollmentArg struct {
	EnrollmentID string
	DecidedBy    string
}

func (SettleEnrollmentArg) Kind() string { return "ledger.settle_enrollment" }

// SettleEnrollmentWorker is the cross-service hop, modelled on the
// compliance evaluation job: a system passport, an idempotent RPC, and a
// bounded retry. The RPC is a no-op for an invited enrollment — those are
// still promoted by an admin's own decision, and this job simply confirms
// what that decision already did.
type SettleEnrollmentWorker struct {
	river.WorkerDefaults[SettleEnrollmentArg]
	Moka   *client.MokaClient
	Logger *logger.Logger
}

func (w *SettleEnrollmentWorker) Work(ctx context.Context, job *river.Job[SettleEnrollmentArg]) error {
	if job.Attempt >= maxSettleAttempts {
		w.Logger.Error(ctx, "Ledger.SettleEnrollmentWorker", "giving up on enrollment settlement",
			"enrollment_id", job.Args.EnrollmentID)
		return nil
	}

	ctx = passport.AddToContext(ctx, passport.SystemPassport())

	res, err := w.Moka.User.Admin.SettleEnrollmentGuarantee(ctx, &userv1.SettleEnrollmentGuaranteeRequest{
		Id:        job.Args.EnrollmentID,
		DecidedBy: job.Args.DecidedBy,
	})
	if err != nil {
		// A dead enrollment (cancelled, declined, unknown) will never
		// settle — retrying cannot change that.
		if status.Code(err) == codes.NotFound {
			w.Logger.Error(ctx, "Ledger.SettleEnrollmentWorker", "enrollment is not settleable",
				"enrollment_id", job.Args.EnrollmentID, "error", err)
			return nil
		}
		w.Logger.Error(ctx, "Ledger.SettleEnrollmentWorker", "settlement call failed",
			"enrollment_id", job.Args.EnrollmentID, "error", err)
		return err
	}

	w.Logger.Info(ctx, "Ledger.SettleEnrollmentWorker", "enrollment settled",
		"enrollment_id", res.GetId(), "status", res.GetStatus().String())

	return nil
}
