// Package email is the email delivery seam. SMTP is the live sender, Log the
// dev stand-in used whenever no SMTP host is configured. The auth usecase
// depends only on Sender.
package email

import (
	"context"

	"mokapay.app/core/logger"
)

// Sender delivers a short message to an email address.
type Sender interface {
	Send(ctx context.Context, email, subject, body string) error
}

// Log only logs the message — OTP codes included. It is the whole service's
// email sender when no SMTP host is configured (local dev), so neither
// local nor CI needs a real mail server. It must never carry real user
// traffic outside local/dev.
type Log struct {
	log *logger.Logger
}

func NewLog(log *logger.Logger) Log {
	return Log{log: log}
}

func (l Log) Send(ctx context.Context, email, subject, body string) error {
	l.log.Info(ctx, "email.mock", "to", email, "subject", subject, "body", body)
	return nil
}
