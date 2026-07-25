// Package repo owns the transfer persistence. Every status transition —
// and the hold release that rides the terminal ones — happens inside the
// payments_ext procedures (single-statement CTEs, status-guarded); this
// layer maps outcomes: zero rows on a transition is
// transfer.ErrTransferNotFound (unknown id or wrong state — the caller
// reads back to tell), never a half-applied move.
package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
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

// Add creates the transfer with every money column stamped (the usecase
// already locked the rate and derived the amounts) plus the birth
// timeline row. clientID is an explicit parameter on purpose: the
// passport identity never rides silently inside a payload struct.
func (s Store) Add(ctx context.Context, clientID uuid.UUID, nt transfer.NewTransfer) (transfer.Transfer, error) {
	params := sqlc.AddTransferParams{
		ClientNk:          clientID.String(),
		BeneficiaryID:     toNullUUID(nt.BeneficiaryID),
		RecipientName:     nt.RecipientName,
		RecipientPhone:    nt.RecipientPhone,
		RecipientOperator: sqlc.PaymentOperator(nt.RecipientOperator),
		RecipientCity:     toText(nt.RecipientCity),
		FromCurrency:      sqlc.Currency(nt.FromCurrency),
		FromOperator:      sqlc.PaymentOperator(nt.FromOperator),
		FromAmount:        nt.FromAmount,
		ToCurrency:        sqlc.Currency(nt.ToCurrency),
		ToAmount:          nt.ToAmount,
		FeeAmount:         nt.FeeAmount,
		MarginAmount:      nt.MarginAmount,
		HoldAmount:        nt.HoldAmount,
		Note:              toText(nt.Note),
	}

	feePct, err := numericFromString(fmt.Sprintf("%d.%02d", nt.FeePctBps/100, nt.FeePctBps%100))
	if err != nil {
		return transfer.Transfer{}, fmt.Errorf("encoding fee pct: %w", err)
	}
	params.FeePct = feePct

	if nt.RateMicros != nil {
		rate, err := numericFromString(fmt.Sprintf("%d.%06d", *nt.RateMicros/1_000_000, *nt.RateMicros%1_000_000))
		if err != nil {
			return transfer.Transfer{}, fmt.Errorf("encoding rate: %w", err)
		}
		params.Rate = rate
	}

	row, err := s.sqlc.AddTransfer(ctx, params)
	if err != nil {
		return transfer.Transfer{}, fmt.Errorf("adding transfer: %w", err)
	}

	return toDomain(row)
}

// Get returns the transfer with the given id.
func (s Store) Get(ctx context.Context, id string) (transfer.Transfer, error) {
	row, err := s.sqlc.GetTransfer(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return transfer.Transfer{}, transfer.ErrTransferNotFound
		}
		return transfer.Transfer{}, fmt.Errorf("getting transfer %s: %w", id, err)
	}

	return viewToDomain(row)
}

// List returns transfers matching the filter, newest first.
func (s Store) List(ctx context.Context, f transfer.Filter, limit int32) ([]transfer.Transfer, error) {
	params := sqlc.ListTransfersParams{MaxRows: limit}
	if f.ClientID != nil {
		params.ClientNk = uuid.NullUUID{UUID: *f.ClientID, Valid: true}
	}
	if f.AgentProfileID != nil {
		params.AgentProfileID = uuid.NullUUID{UUID: *f.AgentProfileID, Valid: true}
	}
	if f.Status != nil {
		params.Status = sqlc.NullTransferStatus{TransferStatus: sqlc.TransferStatus(*f.Status), Valid: true}
	}

	rows, err := s.sqlc.ListTransfers(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("listing transfers: %w", err)
	}

	out := make([]transfer.Transfer, 0, len(rows))
	for _, row := range rows {
		t, err := viewToDomain(row)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}

	return out, nil
}

// ListEvents returns the transfer's timeline, oldest first.
func (s Store) ListEvents(ctx context.Context, transferID string) ([]transfer.Event, error) {
	rows, err := s.sqlc.ListTransferEvents(ctx, transferID)
	if err != nil {
		return nil, fmt.Errorf("listing transfer events: %w", err)
	}

	out := make([]transfer.Event, 0, len(rows))
	for _, row := range rows {
		e, err := eventToDomain(row)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}

	return out, nil
}

// ListStalePendingMatch returns pending_match transfers untouched since
// the cutoff — the rescue sweep's worklist.
func (s Store) ListStalePendingMatch(ctx context.Context, cutoff time.Time, limit int32) ([]uuid.UUID, error) {
	rows, err := s.sqlc.ListStalePendingMatch(ctx, sqlc.ListStalePendingMatchParams{
		Cutoff:  cutoff,
		MaxRows: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("listing stale pending_match transfers: %w", err)
	}

	out := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		id, err := uuid.Parse(row)
		if err != nil {
			return nil, fmt.Errorf("parsing transfer id %q: %w", row, err)
		}
		out = append(out, id)
	}

	return out, nil
}

// MarkClientPaid: proof move pending_client_transfer →
// pending_agent_transfer, stamping the sender proof.
func (s Store) MarkClientPaid(ctx context.Context, id string, proofFileID uuid.UUID, actor *uuid.UUID) (transfer.Transfer, error) {
	row, err := s.sqlc.MarkTransferClientPaid(ctx, sqlc.MarkTransferClientPaidParams{
		ID:          id,
		ProofFileID: proofFileID.String(),
		Actor:       toNullUUID(actor),
	})
	return transitionResult("marking client paid on", id, row, err)
}

// MarkAgentPaid: proof move pending_agent_transfer →
// pending_client_confirmation, stamping the payout proof.
func (s Store) MarkAgentPaid(ctx context.Context, id string, proofFileID uuid.UUID, actor *uuid.UUID) (transfer.Transfer, error) {
	row, err := s.sqlc.MarkTransferAgentPaid(ctx, sqlc.MarkTransferAgentPaidParams{
		ID:          id,
		ProofFileID: proofFileID.String(),
		Actor:       toNullUUID(actor),
	})
	return transitionResult("marking agent paid on", id, row, err)
}

// Complete: → done + hold release, exactly once (actor nil = the
// auto-confirm timer).
func (s Store) Complete(ctx context.Context, id string, actor *uuid.UUID, reason *string) (transfer.Transfer, error) {
	row, err := s.sqlc.CompleteTransfer(ctx, sqlc.CompleteTransferParams{
		ID:     id,
		Actor:  toNullUUID(actor),
		Reason: toText(reason),
	})
	return transitionResult("completing", id, row, err)
}

// CancelPreAccept: the client's own cancel, only before an agent
// committed (no hold yet).
func (s Store) CancelPreAccept(ctx context.Context, id string, clientID uuid.UUID, reason *string) (transfer.Transfer, error) {
	row, err := s.sqlc.CancelTransferPreAccept(ctx, sqlc.CancelTransferPreAcceptParams{
		ID:       id,
		ClientNk: clientID.String(),
		Reason:   toText(reason),
	})
	return transitionResult("cancelling", id, row, err)
}

// CancelAdmin: the ops escape hatch — any open state, releasing the hold
// when one is placed.
func (s Store) CancelAdmin(ctx context.Context, id string, actor uuid.UUID, reason string) (transfer.Transfer, error) {
	row, err := s.sqlc.CancelTransferAdmin(ctx, sqlc.CancelTransferAdminParams{
		ID:     id,
		Actor:  actor.String(),
		Reason: reason,
	})
	return transitionResult("admin-cancelling", id, row, err)
}

// CancelTimeout: the payment-timeout worker's exit from
// pending_client_transfer, releasing the hold.
func (s Store) CancelTimeout(ctx context.Context, id string, reason string) (transfer.Transfer, error) {
	row, err := s.sqlc.CancelTransferTimeout(ctx, sqlc.CancelTransferTimeoutParams{
		ID:     id,
		Reason: reason,
	})
	return transitionResult("timeout-cancelling", id, row, err)
}

// Expire: terminal expiry from the pre-accept states (rounds/deadline
// exhausted) — never a hold to release.
func (s Store) Expire(ctx context.Context, id string, reason string) (transfer.Transfer, error) {
	row, err := s.sqlc.ExpireTransfer(ctx, sqlc.ExpireTransferParams{
		ID:     id,
		Reason: reason,
	})
	return transitionResult("expiring", id, row, err)
}

// TransferToDomain converts a raw transfers row — shared with the
// matching repo, whose accept/requeue procedures return transfer rows.
func TransferToDomain(r sqlc.Transfer) (transfer.Transfer, error) {
	return toDomain(r)
}

func transitionResult(verb, id string, row sqlc.Transfer, err error) (transfer.Transfer, error) {
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return transfer.Transfer{}, transfer.ErrTransferNotFound
		}
		return transfer.Transfer{}, fmt.Errorf("%s transfer %s: %w", verb, id, err)
	}
	return toDomain(row)
}

func toDomain(r sqlc.Transfer) (transfer.Transfer, error) {
	return build(r.ID, r.Reference, r.ClientNk, r.BeneficiaryFk, r.RecipientName,
		r.RecipientPhone, r.RecipientOperator, r.RecipientCity, r.FromCurrency,
		r.FromOperator, r.FromAmount, r.ToCurrency, r.ToAmount, r.FeePct,
		r.FeeAmount, r.MarginAmount, r.TotalAmount, r.Rate, r.HoldAmount, r.Note,
		r.Status, r.AgentProfileFk, r.MatchRound, r.ProofClientNk, r.ProofAgentNk,
		r.CreatedAt, r.UpdatedAt)
}

func viewToDomain(r sqlc.PaymentsExtTransfer) (transfer.Transfer, error) {
	return build(r.ID, r.Reference, r.ClientNk, r.BeneficiaryFk, r.RecipientName,
		r.RecipientPhone, r.RecipientOperator, r.RecipientCity, r.FromCurrency,
		r.FromOperator, r.FromAmount, r.ToCurrency, r.ToAmount, r.FeePct,
		r.FeeAmount, r.MarginAmount, r.TotalAmount, r.Rate, r.HoldAmount, r.Note,
		r.Status, r.AgentProfileFk, r.MatchRound, r.ProofClientNk, r.ProofAgentNk,
		r.CreatedAt, r.UpdatedAt)
}

func build(
	id, reference, clientNk string, beneficiaryFk uuid.NullUUID,
	recipientName, recipientPhone string, recipientOperator sqlc.PaymentOperator,
	recipientCity pgtype.Text, fromCurrency sqlc.Currency,
	fromOperator sqlc.PaymentOperator, fromAmount int64, toCurrency sqlc.Currency,
	toAmount int64, feePct pgtype.Numeric, feeAmount, marginAmount, totalAmount int64,
	rate pgtype.Numeric, holdAmount int64, note pgtype.Text,
	status sqlc.TransferStatus, agentProfileFk uuid.NullUUID, matchRound int32,
	proofClientNk, proofAgentNk uuid.NullUUID, createdAt, updatedAt time.Time,
) (transfer.Transfer, error) {
	tid, err := uuid.Parse(id)
	if err != nil {
		return transfer.Transfer{}, fmt.Errorf("parsing transfer id %q: %w", id, err)
	}
	client, err := uuid.Parse(clientNk)
	if err != nil {
		return transfer.Transfer{}, fmt.Errorf("parsing client id %q: %w", clientNk, err)
	}
	feePctStr, err := numericToString(feePct)
	if err != nil {
		return transfer.Transfer{}, fmt.Errorf("decoding fee pct: %w", err)
	}
	if feePctStr == nil {
		return transfer.Transfer{}, fmt.Errorf("transfer %s has no fee pct", id)
	}
	rateStr, err := numericToString(rate)
	if err != nil {
		return transfer.Transfer{}, fmt.Errorf("decoding rate: %w", err)
	}

	out := transfer.Transfer{
		ID:                tid,
		Reference:         reference,
		ClientID:          client,
		RecipientName:     recipientName,
		RecipientPhone:    recipientPhone,
		RecipientOperator: transfer.Operator(recipientOperator),
		FromCurrency:      transfer.Currency(fromCurrency),
		FromOperator:      transfer.Operator(fromOperator),
		FromAmount:        fromAmount,
		ToCurrency:        transfer.Currency(toCurrency),
		ToAmount:          toAmount,
		FeePct:            *feePctStr,
		FeeAmount:         feeAmount,
		MarginAmount:      marginAmount,
		TotalAmount:       totalAmount,
		Rate:              rateStr,
		HoldAmount:        holdAmount,
		Status:            transfer.Status(status),
		MatchRound:        matchRound,
		CreatedAt:         createdAt,
		UpdatedAt:         updatedAt,
	}
	if beneficiaryFk.Valid {
		out.BeneficiaryID = new(beneficiaryFk.UUID)
	}
	if recipientCity.Valid {
		out.RecipientCity = new(recipientCity.String)
	}
	if note.Valid {
		out.Note = new(note.String)
	}
	if agentProfileFk.Valid {
		out.AgentProfileID = new(agentProfileFk.UUID)
	}
	if proofClientNk.Valid {
		out.ProofClientID = new(proofClientNk.UUID)
	}
	if proofAgentNk.Valid {
		out.ProofAgentID = new(proofAgentNk.UUID)
	}

	return out, nil
}

func eventToDomain(r sqlc.PaymentsExtTransferEvent) (transfer.Event, error) {
	eid, err := uuid.Parse(r.ID)
	if err != nil {
		return transfer.Event{}, fmt.Errorf("parsing event id %q: %w", r.ID, err)
	}
	tid, err := uuid.Parse(r.TransferFk)
	if err != nil {
		return transfer.Event{}, fmt.Errorf("parsing transfer id %q: %w", r.TransferFk, err)
	}

	out := transfer.Event{
		ID:         eid,
		TransferID: tid,
		ToStatus:   transfer.Status(r.ToStatus),
		CreatedAt:  r.CreatedAt,
	}
	if r.ActorNk.Valid {
		out.ActorID = new(r.ActorNk.UUID)
	}
	if r.FromStatus.Valid {
		out.FromStatus = new(transfer.Status(r.FromStatus.TransferStatus))
	}
	if r.Reason.Valid {
		out.Reason = new(r.Reason.String)
	}

	return out, nil
}

// numericFromString builds a pgtype.Numeric from a decimal string —
// amounts and rates never pass through floats.
func numericFromString(s string) (pgtype.Numeric, error) {
	var n pgtype.Numeric
	if err := n.Scan(s); err != nil {
		return pgtype.Numeric{}, err
	}
	return n, nil
}

// numericToString renders a NUMERIC as its decimal text form; nil for
// SQL NULL.
func numericToString(n pgtype.Numeric) (*string, error) {
	if !n.Valid {
		return nil, nil // NULL rate on same-currency corridors
	}
	v, err := n.Value()
	if err != nil {
		return nil, err
	}
	s, ok := v.(string)
	if !ok {
		return nil, fmt.Errorf("unexpected numeric driver value %T", v)
	}
	return new(s), nil
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
