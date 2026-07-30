// Package usecase implements the AML engine: it runs the fixed rule set
// against the metrics snapshot it is handed, appends one audit row per
// rule, and serves the staff review surface (alert lifecycle, account
// risk, dashboard counts). It never blocks anything — detection and
// alerting only.
package usecase

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"mokapay.app/app/services/compliance/internal/business/evaluation"
	"mokapay.app/app/services/compliance/internal/business/evaluation/repo"
	coredb "mokapay.app/core/db"
	"mokapay.app/core/logger"
)

// defaultListLimit caps an unbounded worklist read.
const defaultListLimit = 50

type UseCase struct {
	logger *logger.Logger
	repo   repo.Store
	db     *coredb.Postgres
	cfg    Config
}

func New(l *logger.Logger, db *coredb.Postgres, cfg Config) UseCase {
	return UseCase{
		logger: l,
		repo:   repo.New(db),
		db:     db,
		cfg:    cfg,
	}
}

// EvaluateTransfer runs every applicable rule and appends its audit row —
// triggered or not. The rows of one run commit together: a partially
// recorded evaluation would be a trail that lies about what was checked.
func (u UseCase) EvaluateTransfer(ctx context.Context, snap evaluation.Snapshot) ([]evaluation.RuleEvaluation, error) {
	rows := Evaluate(u.cfg, snap)

	out := make([]evaluation.RuleEvaluation, 0, len(rows))
	if err := u.db.BeginPgx(ctx, func(tx pgx.Tx) error {
		store := u.repo.WithTx(tx)
		out = out[:0]
		for _, row := range rows {
			saved, err := store.Add(ctx, row)
			if err != nil {
				return err
			}
			out = append(out, saved)
		}
		return nil
	}); err != nil {
		u.logger.Error(ctx, "Evaluation.EvaluateTransfer: failed", "transfer", snap.TransferID, "err", err)
		return nil, fmt.Errorf("evaluating transfer %s: %w", snap.TransferID, err)
	}

	return out, nil
}

// ListRuleEvaluations returns everything that was checked on one transfer.
func (u UseCase) ListRuleEvaluations(ctx context.Context, transferID uuid.UUID) ([]evaluation.RuleEvaluation, error) {
	return u.repo.ListByTransfer(ctx, transferID.String())
}

func (u UseCase) GetAlert(ctx context.Context, id uuid.UUID) (evaluation.Alert, error) {
	return u.repo.GetAlert(ctx, id.String())
}

func (u UseCase) ListAlerts(ctx context.Context, f evaluation.AlertFilter, limit int32) ([]evaluation.Alert, error) {
	if limit <= 0 {
		limit = defaultListLimit
	}
	return u.repo.ListAlerts(ctx, f, limit)
}

// UpdateAlertStatus moves an alert through the review lifecycle. Closing
// it (resolved/dismissed) demands a note: a closed alert always says why,
// because that note is what an auditor reads.
func (u UseCase) UpdateAlertStatus(ctx context.Context, data evaluation.UpdateAlert) (evaluation.Alert, error) {
	switch data.Status {
	case evaluation.AlertOpen, evaluation.AlertUnderReview, evaluation.AlertEscalated,
		evaluation.AlertResolved, evaluation.AlertDismissed:
	default:
		return evaluation.Alert{}, evaluation.ErrInvalidStatus
	}

	if data.Status.IsClosing() && (data.ResolutionNote == nil || *data.ResolutionNote == "") {
		return evaluation.Alert{}, evaluation.ErrResolutionRequired
	}

	updated, err := u.repo.UpdateAlert(ctx, data)
	if err != nil {
		if !errors.Is(err, evaluation.ErrAlertNotFound) {
			u.logger.Error(ctx, "Evaluation.UpdateAlertStatus: failed", "alert", data.ID, "err", err)
		}
		return evaluation.Alert{}, err
	}

	return updated, nil
}

// GetAccountRisk returns the 90-day rolling score with its configured
// band applied.
func (u UseCase) GetAccountRisk(ctx context.Context, accountID uuid.UUID) (evaluation.AccountRisk, error) {
	risk, err := u.repo.GetAccountRisk(ctx, accountID.String())
	if err != nil {
		u.logger.Error(ctx, "Evaluation.GetAccountRisk: failed", "account", accountID, "err", err)
		return evaluation.AccountRisk{}, err
	}

	risk.Level = u.cfg.Level(risk.Score)
	return risk, nil
}

func (u UseCase) GetDashboard(ctx context.Context) (evaluation.Dashboard, error) {
	return u.repo.GetDashboard(ctx)
}
