// Package repo owns the commission persistence: the per-transfer accrual
// and the weekly payout batch. Both writes go through payments_ext
// procedures (single-statement, self-idempotent by UNIQUE constraint);
// this layer only maps outcomes — zero rows is a named business error,
// never a half-applied write.
package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"mokapay.app/app/services/payment/internal/business/commission"
	"mokapay.app/app/services/payment/internal/business/transfer"
	"mokapay.app/app/services/payment/internal/sqlc"
	coredb "mokapay.app/core/db"
)

type Store struct {
	sqlc *sqlc.Queries
}

func New(db *coredb.Postgres) Store {
	return Store{sqlc: sqlc.New(db.PgxPool)}
}

// WithTx returns a Store running on the given transaction — how the
// accrual commits with the transition that earned it.
func (s Store) WithTx(tx pgx.Tx) Store {
	return Store{sqlc: s.sqlc.WithTx(tx)}
}

// Accrue writes the transfer's commission. commission.ErrCommissionExists
// = the transfer already has one; the caller treats that as success (a
// replayed completion must never double-book).
func (s Store) Accrue(ctx context.Context, nc commission.NewCommission) (commission.Commission, error) {
	row, err := s.sqlc.AddCommission(ctx, sqlc.AddCommissionParams{
		TransferID:          nc.TransferID.String(),
		AgentProfileID:      nc.AgentProfileID.String(),
		FeeCurrency:         sqlc.Currency(nc.FeeCurrency),
		FeeAmount:           nc.FeeAmount,
		AgentAmount:         nc.AgentAmount,
		PlatformAmount:      nc.PlatformAmount,
		GuaranteeFundAmount: nc.GuaranteeFundAmount,
		MarginCurrency:      sqlc.Currency(nc.MarginCurrency),
		MarginAmount:        nc.MarginAmount,
		PayableAmount:       nc.PayableAmount,
		AccountingWeek:      toDate(nc.AccountingWeek),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return commission.Commission{}, commission.ErrCommissionExists
		}
		return commission.Commission{}, fmt.Errorf("accruing commission for transfer %s: %w", nc.TransferID, err)
	}

	return rowToDomain(row)
}

// GetByTransfer returns the transfer's accrual.
func (s Store) GetByTransfer(ctx context.Context, transferID string) (commission.Commission, error) {
	row, err := s.sqlc.GetCommissionByTransfer(ctx, transferID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return commission.Commission{}, commission.ErrCommissionNotFound
		}
		return commission.Commission{}, fmt.Errorf("getting commission of transfer %s: %w", transferID, err)
	}

	return viewToDomain(row)
}

// List returns accruals matching the filter, newest first.
func (s Store) List(ctx context.Context, f commission.Filter, limit int32) ([]commission.Commission, error) {
	params := sqlc.ListCommissionsParams{MaxRows: limit}
	if f.AgentProfileID != nil {
		params.AgentProfileID = uuid.NullUUID{UUID: *f.AgentProfileID, Valid: true}
	}
	if f.Status != nil {
		params.Status = sqlc.NullCommissionStatus{CommissionStatus: sqlc.CommissionStatus(*f.Status), Valid: true}
	}
	if f.AccountingWeek != nil {
		params.AccountingWeek = toDate(*f.AccountingWeek)
	}

	rows, err := s.sqlc.ListCommissions(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("listing commissions: %w", err)
	}

	out := make([]commission.Commission, 0, len(rows))
	for _, row := range rows {
		c, err := viewToDomain(row)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}

	return out, nil
}

// ListDue returns the weekly run's worklist: unpaid accruals grouped per
// agent per closed accounting week (cutoff excluded — the week in
// progress is never paid).
func (s Store) ListDue(ctx context.Context, weekCutoff time.Time) ([]commission.Due, error) {
	rows, err := s.sqlc.ListDueCommissions(ctx, toDate(weekCutoff))
	if err != nil {
		return nil, fmt.Errorf("listing due commissions: %w", err)
	}

	out := make([]commission.Due, 0, len(rows))
	for _, row := range rows {
		agent, err := uuid.Parse(row.AgentProfileFk)
		if err != nil {
			return nil, fmt.Errorf("parsing agent profile id %q: %w", row.AgentProfileFk, err)
		}
		out = append(out, commission.Due{
			AgentProfileID:  agent,
			AccountingWeek:  row.AccountingWeek.Time,
			CommissionCount: row.CommissionCount,
			TotalAmount:     row.TotalAmount,
		})
	}

	return out, nil
}

// AddBatch links the agent's unpaid accruals of the week to a new batch
// backed by ledgerRequestID. total is the amount already put on that
// request: a mismatch (a concurrent accrual) aborts instead of paying an
// unauthorized number. commission.ErrBatchNotCreated = nothing due, total
// moved, or the week is already batched for this agent.
func (s Store) AddBatch(ctx context.Context, agentProfileID uuid.UUID, week time.Time, total int64, ledgerRequestID uuid.UUID) (commission.Batch, error) {
	row, err := s.sqlc.AddCommissionBatch(ctx, sqlc.AddCommissionBatchParams{
		AgentProfileID:  agentProfileID.String(),
		AccountingWeek:  toDate(week),
		TotalAmount:     total,
		LedgerRequestID: ledgerRequestID.String(),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return commission.Batch{}, commission.ErrBatchNotCreated
		}
		return commission.Batch{}, fmt.Errorf("adding commission batch: %w", err)
	}

	return batchRowToDomain(row)
}

// ListBatches returns payout batches matching the filter, newest week
// first.
func (s Store) ListBatches(ctx context.Context, f commission.BatchFilter, limit int32) ([]commission.Batch, error) {
	params := sqlc.ListCommissionBatchesParams{MaxRows: limit}
	if f.AgentProfileID != nil {
		params.AgentProfileID = uuid.NullUUID{UUID: *f.AgentProfileID, Valid: true}
	}
	if f.Status != nil {
		params.Status = sqlc.NullCommissionStatus{CommissionStatus: sqlc.CommissionStatus(*f.Status), Valid: true}
	}

	rows, err := s.sqlc.ListCommissionBatches(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("listing commission batches: %w", err)
	}

	out := make([]commission.Batch, 0, len(rows))
	for _, row := range rows {
		b, err := batchViewToDomain(row)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}

	return out, nil
}

// rowToDomain converts a raw commissions row (the procedure's return).
// The derived status is not on the base table: a freshly inserted accrual
// is by definition unbatched.
func rowToDomain(r sqlc.Commission) (commission.Commission, error) {
	out, err := buildCommission(r.ID, r.TransferFk, "", r.AgentProfileFk, r.FeeCurrency,
		r.FeeAmount, r.AgentAmount, r.PlatformAmount, r.GuaranteeFundAmount,
		r.MarginCurrency, r.MarginAmount, r.PayableAmount, r.AccountingWeek,
		r.BatchFk, sqlc.CommissionStatus(commission.StatusAccrued), r.CreatedAt, r.UpdatedAt)
	if err != nil {
		return commission.Commission{}, err
	}

	return out, nil
}

func viewToDomain(r sqlc.PaymentsExtCommission) (commission.Commission, error) {
	return buildCommission(r.ID, r.TransferFk, r.TransferReference, r.AgentProfileFk,
		r.FeeCurrency, r.FeeAmount, r.AgentAmount, r.PlatformAmount, r.GuaranteeFundAmount,
		r.MarginCurrency, r.MarginAmount, r.PayableAmount, r.AccountingWeek,
		r.BatchFk, r.Status, r.CreatedAt, r.UpdatedAt)
}

func buildCommission(
	id, transferFk, transferRef, agentFk string, feeCurrency sqlc.Currency,
	fee, agentAmount, platformAmount, guaranteeAmount int64,
	marginCurrency sqlc.Currency, margin, payable int64, week pgtype.Date,
	batchFk uuid.NullUUID, status sqlc.CommissionStatus, createdAt, updatedAt time.Time,
) (commission.Commission, error) {
	cid, err := uuid.Parse(id)
	if err != nil {
		return commission.Commission{}, fmt.Errorf("parsing commission id %q: %w", id, err)
	}
	tid, err := uuid.Parse(transferFk)
	if err != nil {
		return commission.Commission{}, fmt.Errorf("parsing transfer id %q: %w", transferFk, err)
	}
	aid, err := uuid.Parse(agentFk)
	if err != nil {
		return commission.Commission{}, fmt.Errorf("parsing agent profile id %q: %w", agentFk, err)
	}

	out := commission.Commission{
		ID:                  cid,
		TransferID:          tid,
		TransferReference:   transferRef,
		AgentProfileID:      aid,
		FeeCurrency:         transfer.Currency(feeCurrency),
		FeeAmount:           fee,
		AgentAmount:         agentAmount,
		PlatformAmount:      platformAmount,
		GuaranteeFundAmount: guaranteeAmount,
		MarginCurrency:      transfer.Currency(marginCurrency),
		MarginAmount:        margin,
		PayableAmount:       payable,
		AccountingWeek:      week.Time,
		Status:              commission.Status(status),
		CreatedAt:           createdAt,
		UpdatedAt:           updatedAt,
	}
	if batchFk.Valid {
		out.BatchID = new(batchFk.UUID)
	}

	return out, nil
}

func batchRowToDomain(r sqlc.CommissionBatch) (commission.Batch, error) {
	return buildBatch(r.ID, r.AgentProfileFk, r.AccountingWeek, r.CommissionCount,
		r.TotalAmount, r.LedgerRequestFk, "",
		sqlc.CommissionStatus(commission.StatusBatched), r.CreatedAt, r.UpdatedAt)
}

func batchViewToDomain(r sqlc.PaymentsExtCommissionBatch) (commission.Batch, error) {
	return buildBatch(r.ID, r.AgentProfileFk, r.AccountingWeek, r.CommissionCount,
		r.TotalAmount, r.LedgerRequestFk, r.LedgerRequestRef, r.Status, r.CreatedAt, r.UpdatedAt)
}

func buildBatch(
	id, agentFk string, week pgtype.Date, count int32, total int64,
	requestFk, requestRef string, status sqlc.CommissionStatus, createdAt, updatedAt time.Time,
) (commission.Batch, error) {
	bid, err := uuid.Parse(id)
	if err != nil {
		return commission.Batch{}, fmt.Errorf("parsing commission batch id %q: %w", id, err)
	}
	aid, err := uuid.Parse(agentFk)
	if err != nil {
		return commission.Batch{}, fmt.Errorf("parsing agent profile id %q: %w", agentFk, err)
	}
	rid, err := uuid.Parse(requestFk)
	if err != nil {
		return commission.Batch{}, fmt.Errorf("parsing ledger request id %q: %w", requestFk, err)
	}

	return commission.Batch{
		ID:               bid,
		AgentProfileID:   aid,
		AccountingWeek:   week.Time,
		CommissionCount:  count,
		TotalAmount:      total,
		LedgerRequestID:  rid,
		LedgerRequestRef: requestRef,
		Status:           commission.Status(status),
		CreatedAt:        createdAt,
		UpdatedAt:        updatedAt,
	}, nil
}

// toDate renders an accounting week as a SQL DATE — the time part is
// always UTC midnight by construction (commission.AccountingWeek).
func toDate(t time.Time) pgtype.Date {
	return pgtype.Date{Time: t, Valid: true}
}
