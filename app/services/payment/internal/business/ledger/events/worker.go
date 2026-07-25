package events

import (
	"context"

	"github.com/riverqueue/river"
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
