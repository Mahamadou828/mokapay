// Package usecase assembles the AML metrics snapshot for one transfer:
// payment's own rolling aggregates plus the account ages only the user
// service knows. It reads and computes — it changes nothing, and it is
// only ever called from the evaluation job, after the completion committed.
package usecase

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	profilerepo "mokapay.app/app/services/payment/internal/business/agentprofile/repo"
	"mokapay.app/app/services/payment/internal/business/compliance"
	"mokapay.app/app/services/payment/internal/business/compliance/repo"
	transferrepo "mokapay.app/app/services/payment/internal/business/transfer/repo"
	coredb "mokapay.app/core/db"
	"mokapay.app/core/logger"
)

// Config carries the knobs of the snapshot. Timezone is the IANA zone the
// night-time window (23:00–05:00 local) is read in — the market's local
// clock, not the server's.
type Config struct {
	Timezone string
}

type UseCase struct {
	logger    *logger.Logger
	metrics   repo.Store
	transfers transferrepo.Store
	profiles  profilerepo.Store
	accounts  Accounts
	cfg       Config
}

func New(l *logger.Logger, db *coredb.Postgres, accounts Accounts, cfg Config) UseCase {
	return UseCase{
		logger:    l,
		metrics:   repo.New(db),
		transfers: transferrepo.New(db),
		profiles:  profilerepo.New(db),
		accounts:  accounts,
		cfg:       cfg,
	}
}

// Snapshot builds the whole evaluation input for one transfer, as of now.
//
// Every subject is assembled or the call fails: a partial snapshot would
// be evaluated against thresholds it cannot honestly meet — an unresolved
// account age of zero, in particular, reads as "opened today" and would
// trip the new-account rule on an account of any age. The caller retries.
func (u UseCase) Snapshot(ctx context.Context, transferID uuid.UUID) (compliance.Snapshot, error) {
	t, err := u.transfers.Get(ctx, transferID.String())
	if err != nil {
		return compliance.Snapshot{}, err
	}

	now := time.Now()

	senderVolumes, err := u.metrics.SenderVolumes(ctx, t.ClientID, u.cfg.Timezone)
	if err != nil {
		return compliance.Snapshot{}, err
	}
	senderAge, err := u.accountAgeDays(ctx, t.ClientID, now)
	if err != nil {
		return compliance.Snapshot{}, err
	}

	distinctSenders, err := u.metrics.RecipientDistinctSenders(ctx, t.RecipientPhone)
	if err != nil {
		return compliance.Snapshot{}, err
	}

	snap := compliance.Snapshot{
		TransferID: t.ID,
		// The transfer's own USD value is its hold: the client's full
		// total, converted at the rate locked on the transfer itself.
		Sender: compliance.NewAccountMetrics(t.ClientID, t.HoldAmount, senderVolumes, senderAge),
		Recipient: compliance.RecipientMetrics{
			Phone:             t.RecipientPhone,
			DistinctSenders7d: distinctSenders,
		},
	}

	// No assigned agent: the agent rules have no subject to be recorded
	// against, and compliance skips them rather than inventing one.
	if t.AgentProfileID == nil {
		return snap, nil
	}

	// Compliance keys its audit rows on user accounts, so the agent travels
	// as their account id — never the payment-local profile id.
	profile, err := u.profiles.Get(ctx, t.AgentProfileID.String())
	if err != nil {
		return compliance.Snapshot{}, err
	}
	agentVolumes, err := u.metrics.AgentVolumes(ctx, profile.ID, u.cfg.Timezone)
	if err != nil {
		return compliance.Snapshot{}, err
	}
	agentAge, err := u.accountAgeDays(ctx, profile.AccountID, now)
	if err != nil {
		return compliance.Snapshot{}, err
	}
	snap.Agent = new(compliance.NewAccountMetrics(profile.AccountID, t.HoldAmount, agentVolumes, agentAge))

	return snap, nil
}

// accountAgeDays is whole days since the account was opened; an account
// created in the future (clock skew between services) reads as zero rather
// than negative.
func (u UseCase) accountAgeDays(ctx context.Context, accountID uuid.UUID, now time.Time) (int32, error) {
	createdAt, err := u.accounts.CreatedAt(ctx, accountID)
	if err != nil {
		return 0, fmt.Errorf("resolving account age: %w", err)
	}

	days := int64(now.Sub(createdAt).Hours() / 24)
	if days < 0 {
		return 0, nil
	}

	return int32(days), nil
}
