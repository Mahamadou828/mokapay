package webpush

import (
	"context"

	"mokapay.app/app/services/user/internal/business/notification"
	"mokapay.app/core/logger"
)

// Log is the dev stand-in sender used when no VAPID keys are configured
// (same convention as the SMS mock): the payload lands in the logs, never
// on a device.
type Log struct {
	logger *logger.Logger
}

func NewLog(l *logger.Logger) Log {
	return Log{logger: l}
}

// Send implements usecase.PushSender.
func (l Log) Send(ctx context.Context, sub notification.PushSubscription, payload []byte) error {
	l.logger.Info(ctx, "web push (log sender)",
		"endpoint", sub.Endpoint,
		"account_id", sub.AccountID.String(),
		"payload", string(payload),
	)
	return nil
}
