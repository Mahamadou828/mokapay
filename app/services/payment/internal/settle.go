package internal

import (
	"context"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	commissionuc "mokapay.app/app/services/payment/internal/business/commission/usecase"
	transferevents "mokapay.app/app/services/payment/internal/business/transfer/events"
	"mokapay.app/core/db"
)

// SettleCommissions is the manual/ops entry point of the weekly payout —
// the same UseCase.SettleDue the Tuesday periodic job runs, so the two can
// never drift apart. Safe to run at any time and any number of times: one
// payout per agent per accounting week is a UNIQUE constraint, not a
// convention, which is why this service has (and must keep) no
// reconciliation script.
func SettleCommissions() error {
	ctx := context.Background()
	cfg := newConfig()
	log := newLogger()

	pg := db.New(cfg.DB)
	if err := pg.Open(); err != nil {
		log.Error(ctx, "settle-commissions", "failed to open database", "error", err)
		return err
	}
	defer pg.Close()

	// The run notifies each agent through the same River queue the service
	// uses; this client only inserts (no workers, never started), so the
	// jobs are picked up by the running service.
	riverClient, err := river.NewClient(riverpgxv5.New(pg.PgxPool), &river.Config{})
	if err != nil {
		log.Error(ctx, "settle-commissions", "failed to init river client", "error", err)
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	res, err := commissionuc.New(log, pg, transferevents.NewTransferJobs(log, riverClient)).
		SettleDue(ctx, time.Now())
	if err != nil {
		log.Error(ctx, "settle-commissions", "error", err)
		return err
	}

	log.Info(ctx, "settle-commissions", "status", "complete",
		"batches", res.Batches, "total", res.Total, "skipped", res.Skipped)

	return nil
}
