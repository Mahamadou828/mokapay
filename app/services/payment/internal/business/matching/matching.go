// Package matching holds the broadcast-matching domain types: offers
// (one round goes to the whole shortlist at once, first accept wins) and
// the pre-filtered candidates the scorer ranks. The reservation happens
// at ACCEPT, not at offer — which is what makes broadcasting safe.
package matching

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"mokapay.app/app/services/payment/internal/business/agentprofile"
	"mokapay.app/app/services/payment/internal/business/transfer"
)

// ErrOfferGone covers an unknown offer and one no longer live alike
// (lost the race, expired, voided) — the caller reads the offer back to
// tell the agent which.
var ErrOfferGone = errors.New("offer no longer available")

// ErrInsufficientAvailable is the accept-time feasibility failure: the
// offer was live but the agent can no longer cover the reservation.
var ErrInsufficientAvailable = errors.New("available balance cannot cover the hold")

// ErrNoCandidates: the pre-filter came back empty this round — the
// match worker snoozes and retries until the deadline expires the
// transfer.
var ErrNoCandidates = errors.New("no eligible agents right now")

// EventOfferReceived is the event_type a broadcast round announces itself
// with — the notification job and the live websocket push carry the same
// string, so a frontend keys off one vocabulary whichever channel it hears
// on first.
const EventOfferReceived = "transfer.offer_received"

// OfferStatus values mirror the stored offer_status enum.
type OfferStatus string

const (
	OfferStatusOffered  OfferStatus = "offered"
	OfferStatusAccepted OfferStatus = "accepted"
	OfferStatusDeclined OfferStatus = "declined"
	OfferStatusVoided   OfferStatus = "voided" // a sibling won the race
	OfferStatusExpired  OfferStatus = "expired"
)

// Offer is one agent's slot in a broadcast round.
type Offer struct {
	ID             uuid.UUID
	TransferID     uuid.UUID
	AgentProfileID uuid.UUID
	MatchRound     int32
	Score          int32
	Status         OfferStatus
	ExpiresAt      time.Time
	DecidedAt      *time.Time
	CreatedAt      time.Time
}

// AgentOffer is the inbox read model: the offer plus the money summary
// the agent decides on.
type AgentOffer struct {
	Offer
	TransferReference string
	FromCurrency      transfer.Currency
	FromOperator      transfer.Operator
	FromAmount        int64
	ToCurrency        transfer.Currency
	RecipientOperator transfer.Operator
	RecipientCity     *string
	ToAmount          int64
	TotalAmount       int64
	HoldAmount        int64
	Note              *string
}

// Candidate is one pre-filtered agent, carrying everything the scorer
// needs. The hard gates (active, funds, both wallet ends, not the
// client, hasn't declined) already ran in SQL.
type Candidate struct {
	AgentProfileID uuid.UUID
	AccountID      uuid.UUID
	Level          agentprofile.Level
	City           *string
	Available      int64
	DoneCount      int64
	DecidedCount   int64
	ActiveLoad     int64
	DeclinedCount  int64
}

// CandidateQuery are the pre-filter inputs, all derived from the
// transfer being matched.
type CandidateQuery struct {
	TransferID   uuid.UUID
	ClientID     uuid.UUID
	HoldAmount   int64
	FromOperator transfer.Operator
	FromCurrency transfer.Currency
	ToOperator   transfer.Operator
	ToCurrency   transfer.Currency
	Limit        int32
}

// ScoredAgent pairs a candidate with its score for the offer batch.
type ScoredAgent struct {
	AgentProfileID uuid.UUID
	Score          int32
}
