// Package events owns the commission River topology: the weekly payout
// job and the Tuesday schedule that triggers it. There is no polling loop
// and no reconciliation script — the run is idempotent by the
// (agent, accounting_week) UNIQUE constraint, so a missed Tuesday is
// simply paid by the next one.
package events

import (
	"context"
	"time"

	"github.com/riverqueue/river"
	commissionuc "mokapay.app/app/services/payment/internal/business/commission/usecase"
	"mokapay.app/core/logger"
)

// SettleArg runs one weekly payout pass over every closed accounting week
// that still has unpaid accruals.
type SettleArg struct{}

func (SettleArg) Kind() string { return "commission.settle_weekly" }

// SettleWorker is the body of the Tuesday job — the same entry point the
// `settle-commissions` CLI calls, so the manual re-run and the scheduled
// one can never drift apart.
type SettleWorker struct {
	river.WorkerDefaults[SettleArg]
	UC     commissionuc.UseCase
	Logger *logger.Logger
}

func (w *SettleWorker) Work(ctx context.Context, _ *river.Job[SettleArg]) error {
	if _, err := w.UC.SettleDue(ctx, time.Now()); err != nil {
		w.Logger.Error(ctx, "Commission.SettleWorker", "weekly settlement failed", err)
		return err
	}
	return nil
}

// WeeklySchedule fires once a week, on Weekday at Hour o'clock UTC — the
// payout cadence (accounting weeks run Tue→Mon and are paid out the
// following Tuesday).
type WeeklySchedule struct {
	Weekday time.Weekday
	Hour    int
}

// Next returns the first matching slot strictly after current.
func (s WeeklySchedule) Next(current time.Time) time.Time {
	u := current.UTC()
	next := time.Date(u.Year(), u.Month(), u.Day(), s.Hour, 0, 0, 0, time.UTC).
		AddDate(0, 0, (int(s.Weekday)-int(u.Weekday())+7)%7)
	if !next.After(u) {
		next = next.AddDate(0, 0, 7)
	}

	return next
}
