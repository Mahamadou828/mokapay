// Package repo owns the offer persistence and the candidate pre-filter.
// The accept race lives in payments_ext.accept_offer — a single gated
// statement where a failed guard means zero side effects (an offer is
// never burned by a failed accept).
package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"mokapay.app/app/services/payment/internal/business/agentprofile"
	"mokapay.app/app/services/payment/internal/business/matching"
	"mokapay.app/app/services/payment/internal/business/transfer"
	transferrepo "mokapay.app/app/services/payment/internal/business/transfer/repo"
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

// Candidates runs the pre-filter: hard gates in SQL, strongest first.
func (s Store) Candidates(ctx context.Context, q matching.CandidateQuery) ([]matching.Candidate, error) {
	rows, err := s.sqlc.ListMatchCandidates(ctx, sqlc.ListMatchCandidatesParams{
		HoldAmount:   q.HoldAmount,
		ClientNk:     q.ClientID.String(),
		FromOperator: sqlc.PaymentOperator(q.FromOperator),
		FromCurrency: sqlc.Currency(q.FromCurrency),
		ToOperator:   sqlc.PaymentOperator(q.ToOperator),
		ToCurrency:   sqlc.Currency(q.ToCurrency),
		TransferID:   q.TransferID.String(),
		MaxRows:      q.Limit,
	})
	if err != nil {
		return nil, fmt.Errorf("listing match candidates: %w", err)
	}

	out := make([]matching.Candidate, 0, len(rows))
	for _, row := range rows {
		id, err := uuid.Parse(row.ID)
		if err != nil {
			return nil, fmt.Errorf("parsing candidate profile id %q: %w", row.ID, err)
		}
		account, err := uuid.Parse(row.AccountNk)
		if err != nil {
			return nil, fmt.Errorf("parsing candidate account id %q: %w", row.AccountNk, err)
		}
		c := matching.Candidate{
			AgentProfileID: id,
			AccountID:      account,
			Level:          agentprofile.Level(row.Level),
			Available:      row.Available,
			DoneCount:      row.DoneCount,
			DecidedCount:   row.DecidedCount,
			ActiveLoad:     row.ActiveLoad,
			DeclinedCount:  row.DeclinedCount,
		}
		if row.City.Valid {
			c.City = new(row.City.String)
		}
		out = append(out, c)
	}

	return out, nil
}

// OfferBatch broadcasts one round: transfer → pending_agent_approval and
// the whole batch inserted in one statement. Empty result = the transfer
// left pending_match meanwhile (raced with a cancel/expire) — the caller
// drops the round.
func (s Store) OfferBatch(ctx context.Context, transferID uuid.UUID, agents []matching.ScoredAgent, expiresAt time.Time) ([]matching.Offer, error) {
	ids := make([]string, 0, len(agents))
	scores := make([]int32, 0, len(agents))
	for _, a := range agents {
		ids = append(ids, a.AgentProfileID.String())
		scores = append(scores, a.Score)
	}

	rows, err := s.sqlc.OfferTransfer(ctx, sqlc.OfferTransferParams{
		TransferID:      transferID.String(),
		AgentProfileIds: ids,
		Scores:          scores,
		ExpiresAt:       expiresAt,
	})
	if err != nil {
		return nil, fmt.Errorf("offering transfer %s: %w", transferID, err)
	}

	out := make([]matching.Offer, 0, len(rows))
	for _, row := range rows {
		o, err := offerToDomain(row)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}

	return out, nil
}

// Accept is THE race: reservation placed, offer accepted, transfer
// assigned, siblings voided — one statement, zero side effects on any
// failed guard. matching.ErrOfferGone = lost/expired/withdrawn OR
// insufficient available; the caller reads the offer back to tell.
func (s Store) Accept(ctx context.Context, offerID, agentProfileID, actor uuid.UUID) (transfer.Transfer, error) {
	row, err := s.sqlc.AcceptOffer(ctx, sqlc.AcceptOfferParams{
		OfferID:        offerID.String(),
		AgentProfileID: agentProfileID.String(),
		Actor:          actor.String(),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return transfer.Transfer{}, matching.ErrOfferGone
		}
		return transfer.Transfer{}, fmt.Errorf("accepting offer %s: %w", offerID, err)
	}

	return transferrepo.TransferToDomain(row)
}

// Decline takes the agent out of this transfer for good (the pre-filter
// excludes declined agents on every later round).
func (s Store) Decline(ctx context.Context, offerID, agentProfileID uuid.UUID) (matching.Offer, error) {
	row, err := s.sqlc.DeclineOffer(ctx, sqlc.DeclineOfferParams{
		OfferID:        offerID.String(),
		AgentProfileID: agentProfileID.String(),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return matching.Offer{}, matching.ErrOfferGone
		}
		return matching.Offer{}, fmt.Errorf("declining offer %s: %w", offerID, err)
	}

	return offerToDomain(row)
}

// ExpireDue expires the transfer's overdue live offers.
func (s Store) ExpireDue(ctx context.Context, transferID uuid.UUID) ([]matching.Offer, error) {
	rows, err := s.sqlc.ExpireDueOffers(ctx, transferID.String())
	if err != nil {
		return nil, fmt.Errorf("expiring offers of transfer %s: %w", transferID, err)
	}

	out := make([]matching.Offer, 0, len(rows))
	for _, row := range rows {
		o, err := offerToDomain(row)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}

	return out, nil
}

// Requeue sends an offer-exhausted transfer back to the matching queue
// (round++). transfer.ErrTransferNotFound = an accept won the round or
// the transfer already left pending_agent_approval.
func (s Store) Requeue(ctx context.Context, transferID uuid.UUID) (transfer.Transfer, error) {
	row, err := s.sqlc.RequeueUnmatchedTransfer(ctx, transferID.String())
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return transfer.Transfer{}, transfer.ErrTransferNotFound
		}
		return transfer.Transfer{}, fmt.Errorf("requeueing transfer %s: %w", transferID, err)
	}

	return transferrepo.TransferToDomain(row)
}

// Get returns one offer.
func (s Store) Get(ctx context.Context, id string) (matching.Offer, error) {
	row, err := s.sqlc.GetOffer(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return matching.Offer{}, matching.ErrOfferGone
		}
		return matching.Offer{}, fmt.Errorf("getting offer %s: %w", id, err)
	}

	return offerToDomain(sqlc.Offer{
		ID:             row.ID,
		TransferFk:     row.TransferFk,
		AgentProfileFk: row.AgentProfileFk,
		MatchRound:     row.MatchRound,
		Score:          row.Score,
		Status:         row.Status,
		ExpiresAt:      row.ExpiresAt,
		DecidedAt:      row.DecidedAt,
		CreatedAt:      row.CreatedAt,
	})
}

// ListForAgent returns the agent's offers, soonest-expiring first; a nil
// status lists everything.
func (s Store) ListForAgent(ctx context.Context, agentProfileID uuid.UUID, status *matching.OfferStatus, limit int32) ([]matching.AgentOffer, error) {
	params := sqlc.ListAgentOffersParams{
		AgentProfileID: agentProfileID.String(),
		MaxRows:        limit,
	}
	if status != nil {
		params.Status = sqlc.NullOfferStatus{OfferStatus: sqlc.OfferStatus(*status), Valid: true}
	}

	rows, err := s.sqlc.ListAgentOffers(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("listing agent offers: %w", err)
	}

	out := make([]matching.AgentOffer, 0, len(rows))
	for _, row := range rows {
		base, err := offerToDomain(sqlc.Offer{
			ID:             row.ID,
			TransferFk:     row.TransferFk,
			AgentProfileFk: row.AgentProfileFk,
			MatchRound:     row.MatchRound,
			Score:          row.Score,
			Status:         row.Status,
			ExpiresAt:      row.ExpiresAt,
			DecidedAt:      row.DecidedAt,
			CreatedAt:      row.CreatedAt,
		})
		if err != nil {
			return nil, err
		}
		ao := matching.AgentOffer{
			Offer:             base,
			TransferReference: row.TransferReference,
			FromCurrency:      transfer.Currency(row.FromCurrency),
			FromOperator:      transfer.Operator(row.FromOperator),
			FromAmount:        row.FromAmount,
			ToCurrency:        transfer.Currency(row.ToCurrency),
			RecipientOperator: transfer.Operator(row.RecipientOperator),
			ToAmount:          row.ToAmount,
			TotalAmount:       row.TotalAmount,
			HoldAmount:        row.HoldAmount,
		}
		if row.RecipientCity.Valid {
			ao.RecipientCity = new(row.RecipientCity.String)
		}
		if row.Note.Valid {
			ao.Note = new(row.Note.String)
		}
		out = append(out, ao)
	}

	return out, nil
}

func offerToDomain(o sqlc.Offer) (matching.Offer, error) {
	id, err := uuid.Parse(o.ID)
	if err != nil {
		return matching.Offer{}, fmt.Errorf("parsing offer id %q: %w", o.ID, err)
	}
	tid, err := uuid.Parse(o.TransferFk)
	if err != nil {
		return matching.Offer{}, fmt.Errorf("parsing transfer id %q: %w", o.TransferFk, err)
	}
	agent, err := uuid.Parse(o.AgentProfileFk)
	if err != nil {
		return matching.Offer{}, fmt.Errorf("parsing agent profile id %q: %w", o.AgentProfileFk, err)
	}

	out := matching.Offer{
		ID:             id,
		TransferID:     tid,
		AgentProfileID: agent,
		MatchRound:     o.MatchRound,
		Score:          o.Score,
		Status:         matching.OfferStatus(o.Status),
		ExpiresAt:      o.ExpiresAt,
		CreatedAt:      o.CreatedAt,
	}
	if o.DecidedAt.Valid {
		out.DecidedAt = new(o.DecidedAt.Time)
	}

	return out, nil
}
