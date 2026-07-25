// Package repo owns the enrollment domain's persistence: the
// enrollment_requests table. Reads go through the users_ext view, writes
// through the status-guarded users_ext procedures. Role flips live in the
// auth repo — the usecase runs both stores on one transaction via WithTx.
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
	"mokapay.app/app/services/user/internal/business/enrollment"
	"mokapay.app/app/services/user/internal/sqlc"
	coredb "mokapay.app/core/db"
)

type Store struct {
	sqlc *sqlc.Queries
}

func New(db *coredb.Postgres) Store {
	return Store{sqlc: sqlc.New(db.PgxPool)}
}

// WithTx returns a Store running on the given transaction — every
// transition that also flips an account role commits atomically with it.
func (s Store) WithTx(tx pgx.Tx) Store {
	return Store{sqlc: s.sqlc.WithTx(tx)}
}

// Add registers the invite. enrollment.ErrEnrollmentPending means the
// account already has a live request.
func (s Store) Add(ctx context.Context, accountID, invitedBy string) (enrollment.Enrollment, error) {
	row, err := s.sqlc.AddEnrollment(ctx, sqlc.AddEnrollmentParams{
		AccountID: accountID,
		InvitedBy: invitedBy,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "enrollment_requests_live_uq" {
			return enrollment.Enrollment{}, enrollment.ErrEnrollmentPending
		}
		return enrollment.Enrollment{}, fmt.Errorf("adding enrollment: %w", err)
	}

	return toDomain(row)
}

// GetLatestByAccount returns the account's most recent request, whatever
// its status.
func (s Store) GetLatestByAccount(ctx context.Context, accountID string) (enrollment.Enrollment, error) {
	row, err := s.sqlc.GetLatestEnrollmentByAccount(ctx, accountID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return enrollment.Enrollment{}, enrollment.ErrEnrollmentNotFound
		}
		return enrollment.Enrollment{}, fmt.Errorf("getting enrollment for account %s: %w", accountID, err)
	}

	return viewToDomain(row)
}

// List returns enrollments matching the filter, newest first.
func (s Store) List(ctx context.Context, f enrollment.Filter) ([]enrollment.Enrollment, error) {
	rows, err := s.sqlc.ListEnrollments(ctx, filterToParams(f))
	if err != nil {
		return nil, fmt.Errorf("listing enrollments: %w", err)
	}

	out := make([]enrollment.Enrollment, 0, len(rows))
	for _, row := range rows {
		e, err := viewToDomain(row)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}

	return out, nil
}

// Accept moves invited → accepted for the owning account, stamping the
// terms. enrollment.ErrEnrollmentNotFound = wrong id, owner or state.
func (s Store) Accept(ctx context.Context, id, accountID, tosVersion string) (enrollment.Enrollment, error) {
	row, err := s.sqlc.AcceptEnrollment(ctx, sqlc.AcceptEnrollmentParams{
		ID:         id,
		AccountID:  accountID,
		TosVersion: tosVersion,
	})
	return transitionResult("accepting", id, row, err)
}

// Decline moves invited → declined for the owning account.
func (s Store) Decline(ctx context.Context, id, accountID string) (enrollment.Enrollment, error) {
	row, err := s.sqlc.DeclineEnrollment(ctx, sqlc.DeclineEnrollmentParams{
		ID:        id,
		AccountID: accountID,
	})
	return transitionResult("declining", id, row, err)
}

// Cancel moves invited/accepted → cancelled (admin action).
func (s Store) Cancel(ctx context.Context, id, decidedBy string) (enrollment.Enrollment, error) {
	row, err := s.sqlc.CancelEnrollment(ctx, sqlc.CancelEnrollmentParams{
		ID:        id,
		DecidedBy: decidedBy,
	})
	return transitionResult("cancelling", id, row, err)
}

// Approve moves accepted → approved (admin action).
func (s Store) Approve(ctx context.Context, id, decidedBy string) (enrollment.Enrollment, error) {
	row, err := s.sqlc.ApproveEnrollment(ctx, sqlc.ApproveEnrollmentParams{
		ID:        id,
		DecidedBy: decidedBy,
	})
	return transitionResult("approving", id, row, err)
}

// transitionResult folds the shared guarded-UPDATE outcome handling: zero
// rows = lost the race or wrong id/owner/state.
func transitionResult(verb, id string, row sqlc.EnrollmentRequest, err error) (enrollment.Enrollment, error) {
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return enrollment.Enrollment{}, enrollment.ErrEnrollmentNotFound
		}
		return enrollment.Enrollment{}, fmt.Errorf("%s enrollment %s: %w", verb, id, err)
	}
	return toDomain(row)
}

// toDomain converts a base-table row (procedures return
// enrollment_requests).
func toDomain(e sqlc.EnrollmentRequest) (enrollment.Enrollment, error) {
	return build(e.ID, e.AccountFk, e.InvitedBy, e.Status, e.TosVersion,
		e.TosAcceptedAt, e.DecidedBy, e.DecidedAt, e.CreatedAt, e.UpdatedAt)
}

// viewToDomain converts a users_ext.enrollment_requests row.
func viewToDomain(e sqlc.UsersExtEnrollmentRequest) (enrollment.Enrollment, error) {
	return build(e.ID, e.AccountFk, e.InvitedBy, e.Status, e.TosVersion,
		e.TosAcceptedAt, e.DecidedBy, e.DecidedAt, e.CreatedAt, e.UpdatedAt)
}

func build(
	id, accountFk, invitedBy string, status sqlc.EnrollmentStatus,
	tosVersion pgtype.Text, tosAcceptedAt pgtype.Timestamptz,
	decidedBy uuid.NullUUID, decidedAt pgtype.Timestamptz,
	createdAt, updatedAt time.Time,
) (enrollment.Enrollment, error) {
	eid, err := uuid.Parse(id)
	if err != nil {
		return enrollment.Enrollment{}, fmt.Errorf("parsing enrollment id %q: %w", id, err)
	}
	account, err := uuid.Parse(accountFk)
	if err != nil {
		return enrollment.Enrollment{}, fmt.Errorf("parsing account id %q: %w", accountFk, err)
	}
	inviter, err := uuid.Parse(invitedBy)
	if err != nil {
		return enrollment.Enrollment{}, fmt.Errorf("parsing inviter id %q: %w", invitedBy, err)
	}

	out := enrollment.Enrollment{
		ID:        eid,
		AccountID: account,
		InvitedBy: inviter,
		Status:    enrollment.Status(status),
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	}
	if tosVersion.Valid {
		out.TOSVersion = new(tosVersion.String)
	}
	if tosAcceptedAt.Valid {
		out.TOSAcceptedAt = new(tosAcceptedAt.Time)
	}
	if decidedBy.Valid {
		out.DecidedBy = new(decidedBy.UUID)
	}
	if decidedAt.Valid {
		out.DecidedAt = new(decidedAt.Time)
	}

	return out, nil
}

// filterToParams maps the optional status filter to the sqlc null type —
// nil means the filter is absent.
func filterToParams(f enrollment.Filter) sqlc.NullEnrollmentStatus {
	if f.Status == nil {
		return sqlc.NullEnrollmentStatus{}
	}
	return sqlc.NullEnrollmentStatus{EnrollmentStatus: sqlc.EnrollmentStatus(*f.Status), Valid: true}
}
