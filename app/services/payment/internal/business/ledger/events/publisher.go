package events

import (
	"context"

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
