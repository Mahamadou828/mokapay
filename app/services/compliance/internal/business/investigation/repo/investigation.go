// Package repo owns the case persistence: cases and the case_alerts
// junction. Every write goes through a compliance_ext procedure.
package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"mokapay.app/app/services/compliance/internal/business/investigation"
	"mokapay.app/app/services/compliance/internal/sqlc"
	coredb "mokapay.app/core/db"
)

type Store struct {
	sqlc *sqlc.Queries
}

func New(db *coredb.Postgres) Store {
	return Store{sqlc: sqlc.New(db.PgxPool)}
}

// WithTx returns a Store running on the given transaction.
func (s Store) WithTx(tx pgx.Tx) Store {
	return Store{sqlc: s.sqlc.WithTx(tx)}
}

// Add opens a case; the reference comes from the database.
func (s Store) Add(ctx context.Context, data investigation.NewCase) (investigation.Case, error) {
	var summary pgtype.Text
	if data.Summary != nil {
		summary = pgtype.Text{String: *data.Summary, Valid: true}
	}

	row, err := s.sqlc.AddCase(ctx, summary)
	if err != nil {
		return investigation.Case{}, fmt.Errorf("adding case: %w", err)
	}

	return caseToDomain(row.ID, row.Reference, row.Status, row.AssigneeNk, row.Summary,
		row.ClosedAt, row.CreatedAt, row.UpdatedAt)
}

// Get returns the case with the given id.
func (s Store) Get(ctx context.Context, id string) (investigation.Case, error) {
	row, err := s.sqlc.GetCase(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return investigation.Case{}, investigation.ErrCaseNotFound
		}
		return investigation.Case{}, fmt.Errorf("getting case %s: %w", id, err)
	}

	return caseToDomain(row.ID, row.Reference, row.Status, row.AssigneeNk, row.Summary,
		row.ClosedAt, row.CreatedAt, row.UpdatedAt)
}

// List returns cases matching the filter, newest first.
func (s Store) List(ctx context.Context, f investigation.Filter, limit int32) ([]investigation.Case, error) {
	params := sqlc.ListCasesParams{MaxRows: limit}
	if f.Status != nil {
		params.Status = sqlc.NullComplianceCaseStatus{
			ComplianceCaseStatus: sqlc.ComplianceCaseStatus(*f.Status),
			Valid:                true,
		}
	}

	rows, err := s.sqlc.ListCases(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("listing cases: %w", err)
	}

	out := make([]investigation.Case, 0, len(rows))
	for _, row := range rows {
		c, err := caseToDomain(row.ID, row.Reference, row.Status, row.AssigneeNk, row.Summary,
			row.ClosedAt, row.CreatedAt, row.UpdatedAt)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}

	return out, nil
}

// Update applies the partial update; a nil field keeps the stored value.
// investigation.ErrCaseNotFound = unknown or soft-deleted id.
func (s Store) Update(ctx context.Context, data investigation.UpdateCase) (investigation.Case, error) {
	params := sqlc.UpdateCaseParams{ID: data.ID.String()}
	if data.Status != nil {
		params.Status = sqlc.NullComplianceCaseStatus{
			ComplianceCaseStatus: sqlc.ComplianceCaseStatus(*data.Status),
			Valid:                true,
		}
	}
	if data.AssigneeID != nil {
		params.AssigneeNk = uuid.NullUUID{UUID: *data.AssigneeID, Valid: true}
	}
	if data.Summary != nil {
		params.Summary = pgtype.Text{String: *data.Summary, Valid: true}
	}

	row, err := s.sqlc.UpdateCase(ctx, params)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return investigation.Case{}, investigation.ErrCaseNotFound
		}
		return investigation.Case{}, fmt.Errorf("updating case %s: %w", data.ID, err)
	}

	return caseToDomain(row.ID, row.Reference, row.Status, row.AssigneeNk, row.Summary,
		row.ClosedAt, row.CreatedAt, row.UpdatedAt)
}

// AddAlert links an alert to a case. Idempotent: relinking returns the
// existing membership. investigation.ErrLinkTargetMissing = one of the
// two ids does not exist (the junction's foreign keys catch it).
func (s Store) AddAlert(ctx context.Context, caseID, alertID string) (investigation.Link, error) {
	row, err := s.sqlc.AddCaseAlert(ctx, sqlc.AddCaseAlertParams{
		CaseID:  caseID,
		AlertID: alertID,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return investigation.Link{}, investigation.ErrLinkTargetMissing
		}
		return investigation.Link{}, fmt.Errorf("linking alert %s to case %s: %w", alertID, caseID, err)
	}

	cid, err := uuid.Parse(row.CaseFk)
	if err != nil {
		return investigation.Link{}, fmt.Errorf("parsing case id %q: %w", row.CaseFk, err)
	}
	aid, err := uuid.Parse(row.AlertFk)
	if err != nil {
		return investigation.Link{}, fmt.Errorf("parsing alert id %q: %w", row.AlertFk, err)
	}

	return investigation.Link{CaseID: cid, AlertID: aid, CreatedAt: row.CreatedAt}, nil
}

func caseToDomain(
	id, reference string, status sqlc.ComplianceCaseStatus, assignee uuid.NullUUID,
	summary pgtype.Text, closedAt pgtype.Timestamptz, createdAt, updatedAt time.Time,
) (investigation.Case, error) {
	cid, err := uuid.Parse(id)
	if err != nil {
		return investigation.Case{}, fmt.Errorf("parsing case id %q: %w", id, err)
	}

	out := investigation.Case{
		ID:        cid,
		Reference: reference,
		Status:    investigation.Status(status),
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	}
	if assignee.Valid {
		out.AssigneeID = new(assignee.UUID)
	}
	if summary.Valid {
		out.Summary = new(summary.String)
	}
	if closedAt.Valid {
		out.ClosedAt = new(closedAt.Time)
	}

	return out, nil
}
