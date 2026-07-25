// Package usecase implements the broadcast matching engine: pre-filter
// in SQL, weighted scoring in Go, one offer batch to the whole shortlist
// (first accept wins), expiry → requeue → re-match, and the terminal
// expiry when rounds or the deadline run out. Timers are River jobs
// scheduled in the same transaction as the transition that starts them.
package usecase

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"mokapay.app/app/services/payment/internal/business/agentprofile"
	profilerepo "mokapay.app/app/services/payment/internal/business/agentprofile/repo"
	"mokapay.app/app/services/payment/internal/business/matching"
	"mokapay.app/app/services/payment/internal/business/matching/repo"
	"mokapay.app/app/services/payment/internal/business/transfer"
	transferrepo "mokapay.app/app/services/payment/internal/business/transfer/repo"
	coredb "mokapay.app/core/db"
	"mokapay.app/core/logger"
)

// Scheduler is the River publisher the engine enqueues through — always
// inside the transaction of the transition that starts the clock.
type Scheduler interface {
	ScheduleMatch(ctx context.Context, tx pgx.Tx, transferID uuid.UUID) error
	ScheduleOffersExpire(ctx context.Context, tx pgx.Tx, transferID uuid.UUID, at time.Time) error
	SchedulePaymentTimeout(ctx context.Context, tx pgx.Tx, transferID uuid.UUID, at time.Time) error
	Notify(ctx context.Context, tx pgx.Tx, to, eventType, title, message string) error
}

// Config carries the matching knobs, all env-resolved.
type Config struct {
	MaxAgents      int32
	OfferTTL       time.Duration
	MaxRounds      int32
	MatchDeadline  time.Duration
	PaymentTimeout time.Duration
}

type UseCase struct {
	logger    *logger.Logger
	db        *coredb.Postgres
	offers    repo.Store
	transfers transferrepo.Store
	profiles  profilerepo.Store
	scheduler Scheduler
	cfg       Config
}

func New(l *logger.Logger, db *coredb.Postgres, scheduler Scheduler, cfg Config) UseCase {
	return UseCase{
		logger:    l,
		db:        db,
		offers:    repo.New(db),
		transfers: transferrepo.New(db),
		profiles:  profilerepo.New(db),
		scheduler: scheduler,
		cfg:       cfg,
	}
}

// RunMatch is the match worker's body: one broadcast round. No-ops when
// the transfer already left pending_match; expires it past the
// rounds/deadline caps; matching.ErrNoCandidates asks the worker to
// snooze and retry.
func (u UseCase) RunMatch(ctx context.Context, transferID uuid.UUID) error {
	t, err := u.transfers.Get(ctx, transferID.String())
	if err != nil {
		if errors.Is(err, transfer.ErrTransferNotFound) {
			return nil // deleted/never existed — nothing to match
		}
		return err
	}
	if t.Status != transfer.StatusPendingMatch {
		return nil // moved on (accepted, cancelled, …) — the job is stale
	}

	// Caps first: a queued transfer past its deadline expires even if
	// candidates exist now.
	if t.MatchRound >= u.cfg.MaxRounds || time.Since(t.CreatedAt) > u.cfg.MatchDeadline {
		return u.expire(ctx, t)
	}

	candidates, err := u.offers.Candidates(ctx, matching.CandidateQuery{
		TransferID:   t.ID,
		ClientID:     t.ClientID,
		HoldAmount:   t.HoldAmount,
		FromOperator: t.FromOperator,
		FromCurrency: t.FromCurrency,
		ToOperator:   t.RecipientOperator,
		ToCurrency:   t.ToCurrency,
		Limit:        u.cfg.MaxAgents * 3, // headroom: scoring may reorder the SQL pre-rank
	})
	if err != nil {
		return err
	}
	if len(candidates) == 0 {
		return matching.ErrNoCandidates
	}

	scored, accounts := u.rank(t, candidates)
	if len(scored) > int(u.cfg.MaxAgents) {
		scored = scored[:u.cfg.MaxAgents]
	}

	expiresAt := time.Now().Add(u.cfg.OfferTTL)
	if err := u.db.BeginPgx(ctx, func(tx pgx.Tx) error {
		batch, err := u.offers.WithTx(tx).OfferBatch(ctx, t.ID, scored, expiresAt)
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			return nil // the transfer left pending_match meanwhile — drop the round
		}
		if err := u.scheduler.ScheduleOffersExpire(ctx, tx, t.ID, expiresAt); err != nil {
			return err
		}
		for _, s := range scored {
			if err := u.scheduler.Notify(ctx, tx, accounts[s.AgentProfileID].String(),
				"transfer.offer_received",
				"Nouvelle offre de transfert",
				fmt.Sprintf("Un transfert %s est disponible.", t.Reference),
			); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		u.logger.Error(ctx, "Matching.RunMatch: offer round failed", "err", err)
		return fmt.Errorf("offering transfer %s: %w", t.ID, err)
	}

	return nil
}

// HandleOffersExpired is the expiry job's body: close the overdue
// offers, and when nobody accepted, requeue + re-match in one commit.
func (u UseCase) HandleOffersExpired(ctx context.Context, transferID uuid.UUID) error {
	if _, err := u.offers.ExpireDue(ctx, transferID); err != nil {
		return err
	}

	if err := u.db.BeginPgx(ctx, func(tx pgx.Tx) error {
		if _, err := u.offers.WithTx(tx).Requeue(ctx, transferID); err != nil {
			if errors.Is(err, transfer.ErrTransferNotFound) {
				return nil // an accept won the round (or the transfer left) — done
			}
			return err
		}
		return u.scheduler.ScheduleMatch(ctx, tx, transferID)
	}); err != nil {
		u.logger.Error(ctx, "Matching.HandleOffersExpired: failed", "err", err)
		return fmt.Errorf("expiring offer round of %s: %w", transferID, err)
	}

	return nil
}

// AcceptOffer : the agent (resolved from the passport account, never the
// payload) takes the transfer — reservation, assignment and sibling
// voiding are one statement; the payment-timeout clock and the client
// notification commit with it.
func (u UseCase) AcceptOffer(ctx context.Context, agentAccountID, offerID uuid.UUID) (transfer.Transfer, error) {
	profile, err := u.profiles.GetByAccount(ctx, agentAccountID.String())
	if err != nil {
		return transfer.Transfer{}, err
	}

	var accepted transfer.Transfer
	if err := u.db.BeginPgx(ctx, func(tx pgx.Tx) error {
		accepted, err = u.offers.WithTx(tx).Accept(ctx, offerID, profile.ID, agentAccountID)
		if err != nil {
			return err
		}
		if err := u.scheduler.SchedulePaymentTimeout(ctx, tx, accepted.ID, time.Now().Add(u.cfg.PaymentTimeout)); err != nil {
			return err
		}
		return u.scheduler.Notify(ctx, tx, accepted.ClientID.String(),
			"transfer.matched",
			"Agent trouvé",
			fmt.Sprintf("Votre transfert %s a été accepté par un agent.", accepted.Reference),
		)
	}); err != nil {
		if errors.Is(err, matching.ErrOfferGone) {
			return transfer.Transfer{}, u.explainGoneOffer(ctx, offerID, profile)
		}
		u.logger.Error(ctx, "Matching.AcceptOffer: failed", "err", err)
		return transfer.Transfer{}, err
	}

	return accepted, nil
}

// DeclineOffer takes the agent out of this transfer for good; when the
// round dies with this decline, the transfer requeues immediately
// instead of waiting for the expiry timer.
func (u UseCase) DeclineOffer(ctx context.Context, agentAccountID, offerID uuid.UUID) (matching.Offer, error) {
	profile, err := u.profiles.GetByAccount(ctx, agentAccountID.String())
	if err != nil {
		return matching.Offer{}, err
	}

	declined, err := u.offers.Decline(ctx, offerID, profile.ID)
	if err != nil {
		return matching.Offer{}, err
	}

	// Best-effort early requeue: zero rows just means live offers remain
	// (or the transfer left) — the expiry job stays the backstop.
	if err := u.db.BeginPgx(ctx, func(tx pgx.Tx) error {
		if _, err := u.offers.WithTx(tx).Requeue(ctx, declined.TransferID); err != nil {
			if errors.Is(err, transfer.ErrTransferNotFound) {
				return nil
			}
			return err
		}
		return u.scheduler.ScheduleMatch(ctx, tx, declined.TransferID)
	}); err != nil {
		u.logger.Error(ctx, "Matching.DeclineOffer: early requeue failed", "err", err)
	}

	return declined, nil
}

// ListOffers returns the agent's inbox (default: live offers).
func (u UseCase) ListOffers(ctx context.Context, agentAccountID uuid.UUID, status *matching.OfferStatus, limit int32) ([]matching.AgentOffer, error) {
	profile, err := u.profiles.GetByAccount(ctx, agentAccountID.String())
	if err != nil {
		return nil, err
	}
	if status == nil {
		live := matching.OfferStatusOffered
		status = &live
	}
	if limit <= 0 {
		limit = 50
	}
	return u.offers.ListForAgent(ctx, profile.ID, status, limit)
}

// expire terminally expires a transfer that ran out of rounds or time,
// notifying its owner in the same commit.
func (u UseCase) expire(ctx context.Context, t transfer.Transfer) error {
	if err := u.db.BeginPgx(ctx, func(tx pgx.Tx) error {
		if _, err := u.transfers.WithTx(tx).Expire(ctx, t.ID.String(), "no agent matched in time"); err != nil {
			if errors.Is(err, transfer.ErrTransferNotFound) {
				return nil // raced with an accept/cancel — fine
			}
			return err
		}
		return u.scheduler.Notify(ctx, tx, t.ClientID.String(),
			"transfer.expired",
			"Transfert expiré",
			fmt.Sprintf("Aucun agent n'a pu être trouvé pour votre transfert %s.", t.Reference),
		)
	}); err != nil {
		u.logger.Error(ctx, "Matching.RunMatch: expiring failed", "err", err)
		return fmt.Errorf("expiring transfer %s: %w", t.ID, err)
	}
	return nil
}

// explainGoneOffer turns the accept's zero rows into the precise error:
// a live offer + short funds is FailedPrecondition territory, anything
// else means the offer is simply gone.
func (u UseCase) explainGoneOffer(ctx context.Context, offerID uuid.UUID, profile agentprofile.AgentProfile) error {
	o, err := u.offers.Get(ctx, offerID.String())
	if err != nil {
		return matching.ErrOfferGone
	}
	if o.Status == matching.OfferStatusOffered && o.ExpiresAt.After(time.Now()) && o.AgentProfileID == profile.ID {
		return matching.ErrInsufficientAvailable
	}
	return matching.ErrOfferGone
}

// rank scores the shortlist and returns it best-first, plus the
// profile→account map the notifications need. Weights (the 7 criteria,
// wallet fit being a gate rather than a score — its 20 points folded
// into guarantee depth and load):
//
//	availability 25 | guarantee depth 30 | success rate 15 (neutral 8
//	cold start) | level 10 | geo 5 | load 15
func (u UseCase) rank(t transfer.Transfer, candidates []matching.Candidate) ([]matching.ScoredAgent, map[uuid.UUID]uuid.UUID) {
	accounts := make(map[uuid.UUID]uuid.UUID, len(candidates))
	scored := make([]matching.ScoredAgent, 0, len(candidates))
	for _, c := range candidates {
		accounts[c.AgentProfileID] = c.AccountID
		scored = append(scored, matching.ScoredAgent{
			AgentProfileID: c.AgentProfileID,
			Score:          score(t, c),
		})
	}
	sort.SliceStable(scored, func(i, j int) bool { return scored[i].Score > scored[j].Score })
	return scored, accounts
}

func score(t transfer.Transfer, c matching.Candidate) int32 {
	var s int32

	// Availability (25) — by current load.
	switch {
	case c.ActiveLoad == 0:
		s += 25
	case c.ActiveLoad <= 2:
		s += 20
	case c.ActiveLoad <= 4:
		s += 15
	default:
		s += 10
	}

	// Guarantee depth (30) — available / (hold+1) ratio ladder: clearing
	// the gate got them here; holding multiples earns trust.
	ratio := c.Available / (t.HoldAmount + 1)
	switch {
	case ratio >= 5:
		s += 30
	case ratio >= 3:
		s += 25
	case ratio >= 1: // >= the hold itself (gate) up to 3x
		s += 19
	default:
		s += 11
	}

	// Success rate (15) — neutral 8 for the cold start (0 decided), so
	// new agents can win early jobs and build a record.
	if c.DecidedCount == 0 {
		s += 8
	} else {
		rate := c.DoneCount * 100 / c.DecidedCount
		switch {
		case rate >= 99:
			s += 15
		case rate >= 97:
			s += 12
		case rate >= 95:
			s += 10
		default:
			s += 5
		}
	}

	// Level (10).
	switch c.Level {
	case agentprofile.LevelN4:
		s += 10
	case agentprofile.LevelN3:
		s += 8
	case agentprofile.LevelN2:
		s += 6
	default:
		s += 4
	}

	// Geo (5) — city match, or no city on the transfer.
	if t.RecipientCity == nil || (c.City != nil && *c.City == *t.RecipientCity) {
		s += 5
	}

	// Load (15) — spreads work toward idle agents (with availability,
	// ~40% of the score is load-balancing).
	switch {
	case c.ActiveLoad == 0:
		s += 15
	case c.ActiveLoad <= 2:
		s += 12
	case c.ActiveLoad <= 4:
		s += 6
	}

	return s
}
