// Package sms is the SMS delivery seam. Provider selection is deferred
// (design doc: DRC routes need real-world testing) — the auth usecase depends
// only on Sender, so wiring a vendor later is a one-file change.
package sms

import (
	"context"

	"mokapay.app/core/logger"
)

// Sender delivers a short text message to an E.164 phone number.
type Sender interface {
	Send(ctx context.Context, phoneNumber string, message string) error
}

// Log is the dev implementation: it only logs the message — including OTP
// codes — so it must never run outside local/dev environments.
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
