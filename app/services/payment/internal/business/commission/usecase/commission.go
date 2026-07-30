// Package usecase owns the commission read models and the weekly payout
// run: group each agent's unpaid accruals of a closed accounting week
// behind one `commission` ledger request, which ops then approves to
// settle. The accrual itself is written by the transfer/dispute flows
// inside the transaction that earned it — see commission.NewAccrual.
package usecase

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	profilerepo "mokapay.app/app/services/payment/internal/business/agentprofile/repo"
	"mokapay.app/app/services/payment/internal/business/commission"
	"mokapay.app/app/services/payment/internal/business/commission/repo"
	"mokapay.app/app/services/payment/internal/business/ledger"
	ledgerrepo "mokapay.app/app/services/payment/internal/business/ledger/repo"
	coredb "mokapay.app/core/db"
	"mokapay.app/core/logger"
)

// Notifier enqueues a user notification inside the caller's transaction —
// same contract as the ledger and transfer publishers.
type Notifier interface {
	Notify(ctx context.Context, tx pgx.Tx, to, eventType, title, message string) error
}

// systemActor is the created_by of a batch's ledger request: the weekly
// run has no human behind it (uuid.Nil already means "system" across this
// service — see sdk/identity).
var systemActor = uuid.Nil

type UseCase struct {
	logger      *logger.Logger
	db          *coredb.Postgres
	commissions repo.Store
	requests    ledgerrepo.Store
	profiles    profilerepo.Store
	notifier    Notifier
}

func New(l *logger.Logger, db *coredb.Postgres, n Notifier) UseCase {
	return UseCase{
		logger:      l,
		db:          db,
		commissions: repo.New(db),
		requests:    ledgerrepo.New(db),
		profiles:    profilerepo.New(db),
		notifier:    n,
	}
}

// Result reports what one weekly run did — the CLI prints it and the
// periodic job logs it.
type Result struct {
	Batches int   // payouts created
	Total   int64 // USD minor units put on ledger requests
	Skipped int   // agent-weeks already batched, or raced by a late accrual
}

// ListForAgent returns the agent's own accruals, newest first. The
// profile is resolved from the passport account — an agent only ever sees
// their own earnings, whatever the payload claims.
func (u UseCase) ListForAgent(ctx context.Context, agentAccountID uuid.UUID, status *commission.Status, limit int32) ([]commission.Commission, error) {
	profile, err := u.profiles.GetByAccount(ctx, agentAccountID.String())
	if err != nil {
		return nil, err
	}

	return u.commissions.List(ctx, commission.Filter{
		AgentProfileID: new(profile.ID),
		Status:         status,
	}, defaultLimit(limit))
}

// ListBatches is the ops payout view (optional agent/status filters).
func (u UseCase) ListBatches(ctx context.Context, f commission.BatchFilter, limit int32) ([]commission.Batch, error) {
	return u.commissions.ListBatches(ctx, f, defaultLimit(limit))
}

// SettleDue is the weekly run, shared by the Tuesday periodic job and the
// `settle-commissions` CLI. It pays every CLOSED accounting week that
// still has unpaid accruals — not just last week — so a missed run heals
// itself on the next one. One transaction per agent-week: a failure
// isolates to that agent instead of poisoning the whole payout.
//
// Re-running is safe by construction: commission_batches_week_uq allows
// one payout per agent per week, so a second run finds nothing to do.
// That constraint is exactly why this service needs no reconciliation
// script — and must never grow one.
func (u UseCase) SettleDue(ctx context.Context, now time.Time) (Result, error) {
	cutoff := commission.AccountingWeek(now)

	due, err := u.commissions.ListDue(ctx, cutoff)
	if err != nil {
		u.logger.Error(ctx, "Commission.SettleDue: listing due failed", "err", err)
		return Result{}, err
	}

	var res Result
	for _, d := range due {
		batch, err := u.settleOne(ctx, d)
		switch {
		case errors.Is(err, commission.ErrBatchNotCreated):
			// The week is already paid for this agent (or a late accrual
			// changed the total mid-flight). Never silently repaid — say so
			// and move on; ops can re-run once the accrual settles.
			u.logger.Info(ctx, "Commission.SettleDue: skipped",
				"agent_profile_id", d.AgentProfileID, "week", d.AccountingWeek.Format(time.DateOnly))
			res.Skipped++
			continue
		case err != nil:
			u.logger.Error(ctx, "Commission.SettleDue: batch failed",
				"agent_profile_id", d.AgentProfileID, "week", d.AccountingWeek.Format(time.DateOnly), "err", err)
			return res, fmt.Errorf("settling commissions of agent %s: %w", d.AgentProfileID, err)
		}

		res.Batches++
		res.Total += batch.TotalAmount
	}

	u.logger.Info(ctx, "Commission.SettleDue: complete",
		"batches", res.Batches, "total", res.Total, "skipped", res.Skipped)

	return res, nil
}

// settleOne pays one agent-week: the ledger request and the batch that
// links the accruals commit together, so a batch always has its request
// and a request always has its rows.
func (u UseCase) settleOne(ctx context.Context, d commission.Due) (commission.Batch, error) {
	profile, err := u.profiles.Get(ctx, d.AgentProfileID.String())
	if err != nil {
		return commission.Batch{}, err
	}

	reason := fmt.Sprintf("commissions for the week of %s", d.AccountingWeek.Format(time.DateOnly))

	var batch commission.Batch
	if err := u.db.BeginPgx(ctx, func(tx pgx.Tx) error {
		// The credit lands on the ops worklist: approving it settles the
		// balance through the one path that may move it.
		req, err := u.requests.WithTx(tx).Add(ctx, ledger.StatusPendingAdminApproval,
			ledger.DirectionInflow, systemActor, ledger.NewRequest{
				AgentProfileID: d.AgentProfileID,
				Type:           ledger.TypeCommission,
				Amount:         d.TotalAmount,
				Reason:         new(reason),
			})
		if err != nil {
			return err
		}

		batch, err = u.commissions.WithTx(tx).AddBatch(ctx, d.AgentProfileID, d.AccountingWeek, d.TotalAmount, req.ID)
		if err != nil {
			return err
		}

		return u.notifier.Notify(ctx, tx, profile.AccountID.String(),
			"commission.batch_created",
			"Commissions de la semaine",
			fmt.Sprintf("Vos commissions de la semaine du %s (%d transfert(s)) sont en cours de versement.",
				d.AccountingWeek.Format(time.DateOnly), d.CommissionCount))
	}); err != nil {
		return commission.Batch{}, err
	}

	return batch, nil
}

func defaultLimit(limit int32) int32 {
	if limit <= 0 {
		return 50
	}
	return limit
}
