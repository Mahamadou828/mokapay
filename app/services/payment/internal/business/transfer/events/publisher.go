// Package events owns the transfer River topology: job args, the
// transactional publisher (jobs enqueue in the SAME tx as the state
// change that starts their clock) and the workers. No polling loops —
// every timer is a scheduled job that no-ops if the state moved on.
package events

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"mokapay.app/core/logger"
)

// MatchArg runs one matching round for the transfer.
type MatchArg struct {
	TransferID string
}

func (MatchArg) Kind() string { return "transfer.match" }

// OffersExpireArg fires at the offer batch's expires_at: expires
// leftovers and requeues or terminally expires the transfer.
type OffersExpireArg struct {
	TransferID string
}

func (OffersExpireArg) Kind() string { return "transfer.offers_expire" }

// PaymentTimeoutArg fires when the client's payment window elapses.
type PaymentTimeoutArg struct {
	TransferID string
}

func (PaymentTimeoutArg) Kind() string { return "transfer.payment_timeout" }

// AutoConfirmArg fires when the client never reacted to the payout
// proof (D11): completes on their behalf.
type AutoConfirmArg struct {
	TransferID string
}

func (AutoConfirmArg) Kind() string { return "transfer.auto_confirm" }

// NotifyArg pushes a user notification through the user service.
type NotifyArg struct {
	To        string
	Title     string
	Message   string
	EventType string
}

func (NotifyArg) Kind() string { return "transfer.notification" }

// TransferJobs is the publisher half: every enqueue is transactional.
type TransferJobs struct {
	river  *river.Client[pgx.Tx]
	logger *logger.Logger
}

func NewTransferJobs(l *logger.Logger, r *river.Client[pgx.Tx]) TransferJobs {
	return TransferJobs{river: r, logger: l}
}

// ScheduleMatch commits the matching job with the transfer (or with the
// requeue that re-opens the round).
func (p TransferJobs) ScheduleMatch(ctx context.Context, tx pgx.Tx, transferID uuid.UUID) error {
	_, err := p.river.InsertTx(ctx, tx, MatchArg{TransferID: transferID.String()}, nil)
	return err
}

// ScheduleOffersExpire fires at the batch's expires_at.
func (p TransferJobs) ScheduleOffersExpire(ctx context.Context, tx pgx.Tx, transferID uuid.UUID, at time.Time) error {
	_, err := p.river.InsertTx(ctx, tx, OffersExpireArg{TransferID: transferID.String()},
		&river.InsertOpts{ScheduledAt: at})
	return err
}

// SchedulePaymentTimeout starts the client's payment clock at accept.
func (p TransferJobs) SchedulePaymentTimeout(ctx context.Context, tx pgx.Tx, transferID uuid.UUID, at time.Time) error {
	_, err := p.river.InsertTx(ctx, tx, PaymentTimeoutArg{TransferID: transferID.String()},
		&river.InsertOpts{ScheduledAt: at})
	return err
}

// ScheduleAutoConfirm starts the confirmation clock at the payout proof.
func (p TransferJobs) ScheduleAutoConfirm(ctx context.Context, tx pgx.Tx, transferID uuid.UUID, at time.Time) error {
	_, err := p.river.InsertTx(ctx, tx, AutoConfirmArg{TransferID: transferID.String()},
		&river.InsertOpts{ScheduledAt: at})
	return err
}

// Notify commits a user notification with the transition it announces.
func (p TransferJobs) Notify(ctx context.Context, tx pgx.Tx, to, eventType, title, message string) error {
	_, err := p.river.InsertTx(ctx, tx, NotifyArg{
		To:        to,
		Title:     title,
		Message:   message,
		EventType: eventType,
	}, nil)
	return err
}
