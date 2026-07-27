// Package repo owns the ledger persistence: ledger_requests and the
// immutable ledger_lines. Every money movement happens inside the
// payments_ext procedures (single-statement CTEs — the profile-row UPDATE
// serializes concurrent movements); this layer only maps outcomes.
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
	"mokapay.app/app/services/payment/internal/business/ledger"
	"mokapay.app/app/services/payment/internal/sqlc"
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

// Add creates the request; an outflow reserves its amount (held +=) in
// the same statement. Status and direction are the usecase's calls, never
// the API caller's: the request lands with its next actor (admin-created
// funding → pending_agent_action, agent withdrawal →
// pending_admin_approval; zero value = initiated), and the direction is
// derived from the type. A closing status is rejected by the store —
// creation can never bypass settlement. ledger.ErrNotFeasible =
// insufficient funds, ineligible profile, or a closing status;
// ledger.ErrReferenceInUse = the referenced entity already has a
// live-or-settled request (read back with GetByReference — the
// reservation of the failed insert rolled back with it).
// createdBy is an explicit parameter on purpose: silently populating it
// on the payload would let a caller forget it exists.
func (s Store) Add(ctx context.Context, status ledger.Status, d ledger.Direction, createdBy uuid.UUID, data ledger.NewRequest) (ledger.LedgerRequest, error) {
	if status == "" {
		status = ledger.StatusInitiated
	}
	params := sqlc.AddLedgerRequestParams{
		AgentProfileID: data.AgentProfileID.String(),
		Type:           sqlc.LedgerRequestType(data.Type),
		Direction:      sqlc.LedgerDirection(d),
		Amount:         data.Amount,
		CreatedBy:      createdBy.String(),
		Status:         sqlc.LedgerRequestStatus(status),
		Reason:         toText(data.Reason),
	}
	if data.Reference != nil {
		params.ReferenceEntityType = sqlc.NullLedgerReferenceType{
			LedgerReferenceType: sqlc.LedgerReferenceType(data.Reference.Type),
			Valid:               true,
		}
		params.ReferenceEntityID = uuid.NullUUID{UUID: data.Reference.ID, Valid: true}
	}

	row, err := s.sqlc.AddLedgerRequest(ctx, params)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ledger.LedgerRequest{}, ledger.ErrNotFeasible
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "ledger_requests_reference_uq" {
			return ledger.LedgerRequest{}, ledger.ErrReferenceInUse
		}
		return ledger.LedgerRequest{}, fmt.Errorf("adding ledger request: %w", err)
	}

	return toDomain(row)
}

// Get returns the request with the given id.
func (s Store) Get(ctx context.Context, id string) (ledger.LedgerRequest, error) {
	row, err := s.sqlc.GetLedgerRequest(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ledger.LedgerRequest{}, ledger.ErrRequestNotFound
		}
		return ledger.LedgerRequest{}, fmt.Errorf("getting ledger request %s: %w", id, err)
	}

	return viewToDomain(row)
}

// GetByReference returns the entity's live-or-settled request — the
// replay lookup after ErrReferenceInUse, and the enrollment→request
// resolution (rejected requests don't count; the slot is free again).
func (s Store) GetByReference(ctx context.Context, ref ledger.Reference) (ledger.LedgerRequest, error) {
	row, err := s.sqlc.GetLedgerRequestByReference(ctx, sqlc.GetLedgerRequestByReferenceParams{
		ReferenceEntityType: sqlc.LedgerReferenceType(ref.Type),
		ReferenceEntityID:   ref.ID.String(),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ledger.LedgerRequest{}, ledger.ErrRequestNotFound
		}
		return ledger.LedgerRequest{}, fmt.Errorf("getting ledger request by reference: %w", err)
	}

	return viewToDomain(row)
}

// List returns requests matching the filter, newest first.
func (s Store) List(ctx context.Context, f ledger.Filter) ([]ledger.LedgerRequest, error) {
	rows, err := s.sqlc.ListLedgerRequests(ctx, filterToParams(f))
	if err != nil {
		return nil, fmt.Errorf("listing ledger requests: %w", err)
	}

	out := make([]ledger.LedgerRequest, 0, len(rows))
	for _, row := range rows {
		r, err := viewToDomain(row)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}

	return out, nil
}

// UpdateStatus moves the request forward between the pending states and
// attaches the proof when given. ledger.ErrRequestNotFound = unknown id or
// not a forward move (a redelivered proof event lands here — idempotent
// for the caller).
func (s Store) UpdateStatus(ctx context.Context, id string, status ledger.Status, proofFileID *uuid.UUID) (ledger.LedgerRequest, error) {
	row, err := s.sqlc.UpdateLedgerRequestStatus(ctx, sqlc.UpdateLedgerRequestStatusParams{
		ID:          id,
		Status:      sqlc.LedgerRequestStatus(status),
		ProofFileNk: toNullUUID(proofFileID),
	})
	return transitionResult("updating", id, row, err)
}

// Reject closes the request from any non-terminal state, releasing an
// outflow's reservation in the same statement.
func (s Store) Reject(ctx context.Context, id, decidedBy string, reason *string) (ledger.LedgerRequest, error) {
	row, err := s.sqlc.RejectLedgerRequest(ctx, sqlc.RejectLedgerRequestParams{
		ID:        id,
		DecidedBy: decidedBy,
		Reason:    toText(reason),
	})
	return transitionResult("rejecting", id, row, err)
}

// Approve settles: request → done, balance moved (outflows consume their
// reservation), one immutable line written — one statement.
// ledger.ErrRequestNotFound = unknown id or not pending_admin_approval;
// the caller reads the request back — done means a retried approval.
func (s Store) Approve(ctx context.Context, id, decidedBy string) (ledger.LedgerRequest, error) {
	row, err := s.sqlc.ApproveLedgerRequest(ctx, sqlc.ApproveLedgerRequestParams{
		ID:        id,
		DecidedBy: decidedBy,
	})
	return transitionResult("approving", id, row, err)
}

// ListLines returns the profile's settlement lines, newest first.
func (s Store) ListLines(ctx context.Context, agentProfileID string, limit int32) ([]ledger.Line, error) {
	rows, err := s.sqlc.ListLedgerLines(ctx, sqlc.ListLedgerLinesParams{
		AgentProfileID: agentProfileID,
		MaxRows:        limit,
	})
	if err != nil {
		return nil, fmt.Errorf("listing ledger lines: %w", err)
	}

	out := make([]ledger.Line, 0, len(rows))
	for _, row := range rows {
		l, err := lineToDomain(row)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}

	return out, nil
}

func transitionResult(verb, id string, row sqlc.LedgerRequest, err error) (ledger.LedgerRequest, error) {
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ledger.LedgerRequest{}, ledger.ErrRequestNotFound
		}
		return ledger.LedgerRequest{}, fmt.Errorf("%s ledger request %s: %w", verb, id, err)
	}
	return toDomain(row)
}

func toDomain(r sqlc.LedgerRequest) (ledger.LedgerRequest, error) {
	return build(r.ID, r.Ref, r.AgentProfileFk, r.Type, r.Direction, r.Amount, r.Status,
		r.ReferenceEntityType, r.ReferenceEntityID, r.ProofFileNk, r.Reason,
		r.CreatedBy, r.DecidedBy, r.DecidedAt, r.CreatedAt, r.UpdatedAt)
}

func viewToDomain(r sqlc.PaymentsExtLedgerRequest) (ledger.LedgerRequest, error) {
	return build(r.ID, r.Ref, r.AgentProfileFk, r.Type, r.Direction, r.Amount, r.Status,
		r.ReferenceEntityType, r.ReferenceEntityID, r.ProofFileNk, r.Reason,
		r.CreatedBy, r.DecidedBy, r.DecidedAt, r.CreatedAt, r.UpdatedAt)
}

func build(
	id, ref, profileFk string, typ sqlc.LedgerRequestType, direction sqlc.LedgerDirection,
	amount int64, status sqlc.LedgerRequestStatus,
	refType sqlc.NullLedgerReferenceType, refID uuid.NullUUID,
	proofFileNk uuid.NullUUID, reason pgtype.Text,
	createdBy string, decidedBy uuid.NullUUID, decidedAt pgtype.Timestamptz,
	createdAt, updatedAt time.Time,
) (ledger.LedgerRequest, error) {
	rid, err := uuid.Parse(id)
	if err != nil {
		return ledger.LedgerRequest{}, fmt.Errorf("parsing ledger request id %q: %w", id, err)
	}
	profile, err := uuid.Parse(profileFk)
	if err != nil {
		return ledger.LedgerRequest{}, fmt.Errorf("parsing agent profile id %q: %w", profileFk, err)
	}
	creator, err := uuid.Parse(createdBy)
	if err != nil {
		return ledger.LedgerRequest{}, fmt.Errorf("parsing creator id %q: %w", createdBy, err)
	}

	out := ledger.LedgerRequest{
		ID:             rid,
		Ref:            ref,
		AgentProfileID: profile,
		Type:           ledger.Type(typ),
		Direction:      ledger.Direction(direction),
		Amount:         amount,
		Status:         ledger.Status(status),
		CreatedBy:      creator,
		CreatedAt:      createdAt,
		UpdatedAt:      updatedAt,
	}
	if refType.Valid && refID.Valid {
		out.Reference = new(ledger.Reference{
			Type: ledger.ReferenceType(refType.LedgerReferenceType),
			ID:   refID.UUID,
		})
	}
	if proofFileNk.Valid {
		out.ProofFileID = new(proofFileNk.UUID)
	}
	if reason.Valid {
		out.Reason = new(reason.String)
	}
	if decidedBy.Valid {
		out.DecidedBy = new(decidedBy.UUID)
	}
	if decidedAt.Valid {
		out.DecidedAt = new(decidedAt.Time)
	}

	return out, nil
}

func lineToDomain(l sqlc.PaymentsExtLedgerLine) (ledger.Line, error) {
	lid, err := uuid.Parse(l.ID)
	if err != nil {
		return ledger.Line{}, fmt.Errorf("parsing ledger line id %q: %w", l.ID, err)
	}
	profile, err := uuid.Parse(l.AgentProfileFk)
	if err != nil {
		return ledger.Line{}, fmt.Errorf("parsing agent profile id %q: %w", l.AgentProfileFk, err)
	}
	request, err := uuid.Parse(l.LedgerRequestFk)
	if err != nil {
		return ledger.Line{}, fmt.Errorf("parsing ledger request id %q: %w", l.LedgerRequestFk, err)
	}

	return ledger.Line{
		ID:             lid,
		AgentProfileID: profile,
		RequestID:      request,
		Amount:         l.Amount,
		BalanceAfter:   l.BalanceAfter,
		CreatedAt:      l.CreatedAt,
	}, nil
}

// filterToParams maps the optional filters to the sqlc null types — nil
// means "not filtering".
func filterToParams(f ledger.Filter) sqlc.ListLedgerRequestsParams {
	var p sqlc.ListLedgerRequestsParams
	if f.Status != nil {
		p.Status = sqlc.NullLedgerRequestStatus{LedgerRequestStatus: sqlc.LedgerRequestStatus(*f.Status), Valid: true}
	}
	if f.Type != nil {
		p.Type = sqlc.NullLedgerRequestType{LedgerRequestType: sqlc.LedgerRequestType(*f.Type), Valid: true}
	}
	if f.AgentProfileID != nil {
		p.AgentProfileID = uuid.NullUUID{UUID: *f.AgentProfileID, Valid: true}
	}
	return p
}

func toNullUUID(id *uuid.UUID) uuid.NullUUID {
	if id == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *id, Valid: true}
}

func toText(s *string) pgtype.Text {
	if s == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *s, Valid: true}
}
