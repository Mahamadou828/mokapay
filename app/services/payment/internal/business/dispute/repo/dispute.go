// Package repo owns the dispute persistence. Opening and resolving are
// payments_ext procedures (single-statement, status-guarded): the freeze
// keeps the hold, the release drops it, the capture crosses it into the
// balance through a settled deduction request. This layer maps outcomes —
// zero rows is dispute.ErrDisputeNotFound (unknown id or wrong state),
// never a half-applied ruling.
package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"mokapay.app/app/services/payment/internal/business/dispute"
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

// WithTx returns a Store running on the given transaction.
func (s Store) WithTx(tx pgx.Tx) Store {
	return Store{sqlc: s.sqlc.WithTx(tx)}
}

// Open freezes the transfer and records the dispute; the hold STAYS held.
// dispute.ErrNotDisputable = unknown transfer or a state with no money at
// risk.
func (s Store) Open(ctx context.Context, nd dispute.NewDispute) (dispute.Dispute, error) {
	row, err := s.sqlc.OpenDispute(ctx, sqlc.OpenDisputeParams{
		TransferID: nd.TransferID.String(),
		Party:      sqlc.DisputeParty(nd.Party),
		OpenedBy:   toNullUUID(nd.OpenedBy),
		Reason:     sqlc.DisputeReason(nd.Reason),
		Details:    toText(nd.Details),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return dispute.Dispute{}, dispute.ErrNotDisputable
		}
		return dispute.Dispute{}, fmt.Errorf("opening dispute on transfer %s: %w", nd.TransferID, err)
	}

	return rowToDomain(row)
}

// Get returns the dispute with the given id.
func (s Store) Get(ctx context.Context, id string) (dispute.Dispute, error) {
	row, err := s.sqlc.GetDispute(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return dispute.Dispute{}, dispute.ErrDisputeNotFound
		}
		return dispute.Dispute{}, fmt.Errorf("getting dispute %s: %w", id, err)
	}

	return viewToDomain(row)
}

// GetOpenByTransfer returns the transfer's live dispute.
func (s Store) GetOpenByTransfer(ctx context.Context, transferID string) (dispute.Dispute, error) {
	row, err := s.sqlc.GetOpenDisputeByTransfer(ctx, transferID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return dispute.Dispute{}, dispute.ErrDisputeNotFound
		}
		return dispute.Dispute{}, fmt.Errorf("getting open dispute of transfer %s: %w", transferID, err)
	}

	return viewToDomain(row)
}

// List returns disputes matching the filter, newest first.
func (s Store) List(ctx context.Context, f dispute.Filter, limit int32) ([]dispute.Dispute, error) {
	params := sqlc.ListDisputesParams{MaxRows: limit}
	if f.Status != nil {
		params.Status = sqlc.NullDisputeStatus{DisputeStatus: sqlc.DisputeStatus(*f.Status), Valid: true}
	}
	if f.TransferID != nil {
		params.TransferID = uuid.NullUUID{UUID: *f.TransferID, Valid: true}
	}
	if f.AgentProfileID != nil {
		params.AgentProfileID = uuid.NullUUID{UUID: *f.AgentProfileID, Valid: true}
	}
	if f.ClientID != nil {
		params.ClientID = uuid.NullUUID{UUID: *f.ClientID, Valid: true}
	}

	rows, err := s.sqlc.ListDisputes(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("listing disputes: %w", err)
	}

	out := make([]dispute.Dispute, 0, len(rows))
	for _, row := range rows {
		d, err := viewToDomain(row)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}

	return out, nil
}

// ResolveRelease applies a ruling that frees the collateral: hold
// released, no balance movement, the transfer lands on terminal.
func (s Store) ResolveRelease(ctx context.Context, id string, resolution dispute.Resolution, terminal transfer.Status, resolvedBy uuid.UUID, note *string) (dispute.Dispute, error) {
	row, err := s.sqlc.ResolveDisputeRelease(ctx, sqlc.ResolveDisputeReleaseParams{
		ID:             id,
		Resolution:     sqlc.DisputeResolution(resolution),
		TerminalStatus: sqlc.TransferStatus(terminal),
		ResolvedBy:     resolvedBy.String(),
		Note:           toText(note),
	})
	return resolveResult("releasing", id, row, err)
}

// ResolveCapture applies the agent-at-fault ruling: the guarantee is
// captured through a settled deduction request and the transfer lands on
// `refunded`.
func (s Store) ResolveCapture(ctx context.Context, id string, resolvedBy uuid.UUID, note *string) (dispute.Dispute, error) {
	row, err := s.sqlc.ResolveDisputeCapture(ctx, sqlc.ResolveDisputeCaptureParams{
		ID:         id,
		ResolvedBy: resolvedBy.String(),
		Note:       toText(note),
	})
	return resolveResult("capturing on", id, row, err)
}

func resolveResult(verb, id string, row sqlc.Dispute, err error) (dispute.Dispute, error) {
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return dispute.Dispute{}, dispute.ErrDisputeNotFound
		}
		return dispute.Dispute{}, fmt.Errorf("%s dispute %s: %w", verb, id, err)
	}
	return rowToDomain(row)
}

// rowToDomain converts a raw disputes row (a procedure's return): it
// carries no joined transfer identity, which the caller already holds.
func rowToDomain(r sqlc.Dispute) (dispute.Dispute, error) {
	return build(r.ID, r.TransferFk, "", "", uuid.NullUUID{}, r.OpenedByParty, r.OpenedBy,
		r.Reason, r.Details, r.FromStatus, r.HoldAmount, r.RefundCurrency, r.RefundAmount,
		r.Status, r.Resolution, r.HoldOutcome, r.ResolutionNote, r.DeductionFk,
		r.ResolvedBy, r.ResolvedAt, r.CreatedAt, r.UpdatedAt)
}

func viewToDomain(r sqlc.PaymentsExtDispute) (dispute.Dispute, error) {
	return build(r.ID, r.TransferFk, r.TransferReference, r.ClientNk, r.AgentProfileFk,
		r.OpenedByParty, r.OpenedBy, r.Reason, r.Details, r.FromStatus, r.HoldAmount,
		r.RefundCurrency, r.RefundAmount, r.Status, r.Resolution, r.HoldOutcome,
		r.ResolutionNote, r.DeductionFk, r.ResolvedBy, r.ResolvedAt, r.CreatedAt, r.UpdatedAt)
}

func build(
	id, transferFk, transferRef, clientNk string, agentFk uuid.NullUUID,
	party sqlc.DisputeParty, openedBy uuid.NullUUID, reason sqlc.DisputeReason,
	details pgtype.Text, fromStatus sqlc.TransferStatus, holdAmount int64,
	refundCurrency sqlc.Currency, refundAmount int64, status sqlc.DisputeStatus,
	resolution sqlc.NullDisputeResolution, outcome sqlc.NullHoldOutcome,
	note pgtype.Text, deductionFk, resolvedBy uuid.NullUUID,
	resolvedAt pgtype.Timestamptz, createdAt, updatedAt time.Time,
) (dispute.Dispute, error) {
	did, err := uuid.Parse(id)
	if err != nil {
		return dispute.Dispute{}, fmt.Errorf("parsing dispute id %q: %w", id, err)
	}
	tid, err := uuid.Parse(transferFk)
	if err != nil {
		return dispute.Dispute{}, fmt.Errorf("parsing transfer id %q: %w", transferFk, err)
	}

	out := dispute.Dispute{
		ID:                did,
		TransferID:        tid,
		TransferReference: transferRef,
		OpenedByParty:     dispute.Party(party),
		Reason:            dispute.Reason(reason),
		FromStatus:        transfer.Status(fromStatus),
		HoldAmount:        holdAmount,
		RefundCurrency:    transfer.Currency(refundCurrency),
		RefundAmount:      refundAmount,
		Status:            dispute.Status(status),
		CreatedAt:         createdAt,
		UpdatedAt:         updatedAt,
	}
	// The procedures return the base row, which carries no client id.
	if clientNk != "" {
		cid, err := uuid.Parse(clientNk)
		if err != nil {
			return dispute.Dispute{}, fmt.Errorf("parsing client id %q: %w", clientNk, err)
		}
		out.ClientID = cid
	}
	if agentFk.Valid {
		out.AgentProfileID = new(agentFk.UUID)
	}
	if openedBy.Valid {
		out.OpenedBy = new(openedBy.UUID)
	}
	if details.Valid {
		out.Details = new(details.String)
	}
	if resolution.Valid {
		out.Resolution = new(dispute.Resolution(resolution.DisputeResolution))
	}
	if outcome.Valid {
		out.HoldOutcome = new(dispute.HoldOutcome(outcome.HoldOutcome))
	}
	if note.Valid {
		out.ResolutionNote = new(note.String)
	}
	if deductionFk.Valid {
		out.DeductionID = new(deductionFk.UUID)
	}
	if resolvedBy.Valid {
		out.ResolvedBy = new(resolvedBy.UUID)
	}
	if resolvedAt.Valid {
		out.ResolvedAt = new(resolvedAt.Time)
	}

	return out, nil
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
