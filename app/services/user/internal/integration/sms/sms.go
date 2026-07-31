// Package sms is the SMS delivery seam. AfricasTalking is the live provider,
// Log the dev stand-in, and Router picks between them per destination so the
// seeded test phones never trigger a real send. The auth usecase depends only
// on Sender.
package sms

import (
	"context"

	"mokapay.app/core/logger"
)

// Sender delivers a short text message to an E.164 phone number.
type Sender interface {
	Send(ctx context.Context, phoneNumber string, message string) error
}

// Log only logs the message — OTP codes included. It serves two roles: the
// whole service's sender when no provider credentials are configured (local
// dev), and, behind a Router, the sender for the seeded test phones in every
// environment. It must never carry real user traffic outside local/dev.
type Log struct {
	log *logger.Logger
}

func NewLog(log *logger.Logger) Log {
	return Log{log: log}
}

func (l Log) Send(ctx context.Context, phoneNumber, message string) error {
	l.log.Info(ctx, "sms.mock", "to", phoneNumber, "message", message)
	return nil
}
