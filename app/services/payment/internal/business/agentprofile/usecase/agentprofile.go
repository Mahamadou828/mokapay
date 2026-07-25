// Package usecase implements the agent-profile lifecycle. Money never
// moves here — balances belong to the ledger domain; this is pure status
// machinery over the profile row.
package usecase

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"mokapay.app/app/services/payment/internal/business/agentprofile"
	"mokapay.app/app/services/payment/internal/business/agentprofile/repo"
	coredb "mokapay.app/core/db"
	"mokapay.app/core/logger"
)

type UseCase struct {
	logger *logger.Logger
	repo   repo.Store
}

func New(l *logger.Logger, db *coredb.Postgres) UseCase {
	return UseCase{
		logger: l,
		repo:   repo.New(db),
	}
}

// Create registers a pending profile. ErrProfileExists passes through —
// idempotent callers (enrollment Accept retries) treat it as
// success-by-lookup.
func (u UseCase) Create(ctx context.Context, np agentprofile.NewAgentProfile) (agentprofile.AgentProfile, error) {
	created, err := u.repo.Add(ctx, np)
	if err != nil {
		if !errors.Is(err, agentprofile.ErrProfileExists) {
			u.logger.Error(ctx, "AgentProfile.Create: failed", "err", err)
		}
		return agentprofile.AgentProfile{}, err
	}
	return created, nil
}

// Get returns the profile by id.
func (u UseCase) Get(ctx context.Context, id uuid.UUID) (agentprofile.AgentProfile, error) {
	return u.repo.Get(ctx, id.String())
}

// GetByAccount returns the profile of a user-service account.
func (u UseCase) GetByAccount(ctx context.Context, accountID uuid.UUID) (agentprofile.AgentProfile, error) {
	return u.repo.GetByAccount(ctx, accountID.String())
}

// Activate moves pending/suspended → active. Activating an active profile
// is a no-op success — enrollment-approve retries depend on it.
func (u UseCase) Activate(ctx context.Context, id uuid.UUID) (agentprofile.AgentProfile, error) {
	return u.transition(ctx, id, agentprofile.StatusActive, agentprofile.StatusPending, agentprofile.StatusSuspended)
}

// Suspend moves active → suspended, idempotently.
func (u UseCase) Suspend(ctx context.Context, id uuid.UUID) (agentprofile.AgentProfile, error) {
	return u.transition(ctx, id, agentprofile.StatusSuspended, agentprofile.StatusActive)
}

// Delete soft-deletes a zero-money profile.
func (u UseCase) Delete(ctx context.Context, id uuid.UUID) error {
	if err := u.repo.Delete(ctx, id.String()); err != nil {
		if !errors.Is(err, agentprofile.ErrProfileNotFound) && !errors.Is(err, agentprofile.ErrProfileHasFunds) {
			u.logger.Error(ctx, "AgentProfile.Delete: failed", "err", err)
		}
		return err
	}
	return nil
}

// transition reads, checks legality (target = no-op success, listed
// sources = allowed, anything else = ErrInvalidTransition) and writes.
func (u UseCase) transition(ctx context.Context, id uuid.UUID, target agentprofile.Status, from ...agentprofile.Status) (agentprofile.AgentProfile, error) {
	current, err := u.repo.Get(ctx, id.String())
	if err != nil {
		return agentprofile.AgentProfile{}, err
	}
	if current.Status == target {
		return current, nil // idempotent no-op
	}

	allowed := false
	for _, f := range from {
		if current.Status == f {
			allowed = true
			break
		}
	}
	if !allowed {
		return agentprofile.AgentProfile{}, agentprofile.ErrInvalidTransition
	}

	updated, err := u.repo.SetStatus(ctx, id.String(), target)
	if err != nil {
		u.logger.Error(ctx, "AgentProfile.transition: failed", "err", err, "target", target)
		return agentprofile.AgentProfile{}, err
	}
	return updated, nil
}
