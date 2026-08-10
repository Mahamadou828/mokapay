// Package events owns the ledger domain's River topology: the candidate
// notification, and the enrollment settlement that a guarantee deposit's
// approval triggers on the user service. Both enqueue in the SAME
// transaction as the ledger transition that earns them, so a request that
// is `done` and a job that will announce it commit together or not at all.
package events

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"mokapay.app/core/client"
	"mokapay.app/core/logger"
)

type LedgerNotification struct {
	riverClient *river.Client[pgx.Tx]
	mokaClient  *client.MokaClient
	logger      *logger.Logger
}

func NewLedgerNotification(l *logger.Logger, r *river.Client[pgx.Tx], c *client.MokaClient) LedgerNotification {
	return LedgerNotification{
		riverClient: r,
		mokaClient:  c,
		logger:      l,
	}
}

func (p LedgerNotification) Notify(ctx context.Context, tx pgx.Tx, to, eventType, title, message string) error {
	if _, err := p.riverClient.InsertTx(
		ctx,
		tx,
		LedgerNotifyArg{
			To:        to,
			Title:     title,
			Message:   message,
			EventType: eventType,
		},
		&river.InsertOpts{},
	); err != nil {
		return err
	}
	return nil
}

// SettleEnrollment hands a settled guarantee deposit back to the user
// service, which owns the enrollment state machine and the account role.
// It commits with the approval itself: for a SELF-SERVE enrollment this
// approval is the promotion decision, so losing the job would leave a
// funded candidate stranded at pending_agent.
//
// decidedBy is the ops user who approved the deposit — the accountable
// human the enrollment records as its decider, since the RPC itself runs
// under a system passport.
func (p LedgerNotification) SettleEnrollment(ctx context.Context, tx pgx.Tx, enrollmentID, decidedBy uuid.UUID) error {
	_, err := p.riverClient.InsertTx(ctx, tx, SettleEnrollmentArg{
		EnrollmentID: enrollmentID.String(),
		DecidedBy:    decidedBy.String(),
	}, &river.InsertOpts{})
	return err
}
