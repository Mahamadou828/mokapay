// Package repo owns the agent-profile persistence. Reads go through the
// payments_ext view (where available is derived), writes through the
// payments_ext procedures. Balance/held are never written here — the
// ledger procedures own every money movement.
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
	"mokapay.app/app/services/payment/internal/business/agentprofile"
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

// Add creates a pending profile. agentprofile.ErrProfileExists means the
// account already has a live one.
func (s Store) Add(ctx context.Context, np agentprofile.NewAgentProfile) (agentprofile.AgentProfile, error) {
	currency := np.Currency
	if currency == "" {
		currency = "USD"
	}
	floor := int64(2000)
	if np.GuaranteeFloor != nil {
		floor = *np.GuaranteeFloor
	}

	row, err := s.sqlc.AddAgentProfile(ctx, sqlc.AddAgentProfileParams{
		AccountID:      np.AccountID.String(),
		Currency:       currency,
		GuaranteeFloor: floor,
		City:           toText(np.City),
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "agent_profiles_account_uq" {
			return agentprofile.AgentProfile{}, agentprofile.ErrProfileExists
		}
		return agentprofile.AgentProfile{}, fmt.Errorf("adding agent profile: %w", err)
	}

	return baseToDomain(row)
}

// Get returns the live profile with the given id.
func (s Store) Get(ctx context.Context, id string) (agentprofile.AgentProfile, error) {
	row, err := s.sqlc.GetAgentProfile(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return agentprofile.AgentProfile{}, agentprofile.ErrProfileNotFound
		}
		return agentprofile.AgentProfile{}, fmt.Errorf("getting agent profile %s: %w", id, err)
	}

	return viewToDomain(row)
}

// GetByAccount returns the live profile of the user-service account.
func (s Store) GetByAccount(ctx context.Context, accountID string) (agentprofile.AgentProfile, error) {
	row, err := s.sqlc.GetAgentProfileByAccount(ctx, accountID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return agentprofile.AgentProfile{}, agentprofile.ErrProfileNotFound
		}
		return agentprofile.AgentProfile{}, fmt.Errorf("getting agent profile for account %s: %w", accountID, err)
	}

	return viewToDomain(row)
}

// SetStatus writes the lifecycle status; transition legality is the
// usecase's concern.
func (s Store) SetStatus(ctx context.Context, id string, status agentprofile.Status) (agentprofile.AgentProfile, error) {
	row, err := s.sqlc.SetAgentProfileStatus(ctx, sqlc.SetAgentProfileStatusParams{
		ID:     id,
		Status: sqlc.AgentProfileStatus(status),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return agentprofile.AgentProfile{}, agentprofile.ErrProfileNotFound
		}
		return agentprofile.AgentProfile{}, fmt.Errorf("setting agent profile %s status: %w", id, err)
	}

	return baseToDomain(row)
}

// Delete soft-deletes a zero-money profile;
// agentprofile.ErrProfileHasFunds when balance or held is non-zero.
func (s Store) Delete(ctx context.Context, id string) error {
	if _, err := s.sqlc.DeleteAgentProfile(ctx, id); err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("deleting agent profile %s: %w", id, err)
		}
		// Zero rows: unknown, already deleted, or funded — read to tell.
		p, gerr := s.Get(ctx, id)
		if gerr != nil {
			return agentprofile.ErrProfileNotFound
		}
		if p.Balance != 0 || p.Held != 0 {
			return agentprofile.ErrProfileHasFunds
		}
		return agentprofile.ErrProfileNotFound
	}
	return nil
}

// baseToDomain converts a base-table row (write procedures return
// agent_profiles); available is derived on the spot.
func baseToDomain(p sqlc.AgentProfile) (agentprofile.AgentProfile, error) {
	return build(p.ID, p.AccountNk, p.Status, p.Level, p.Currency,
		p.Balance, p.Held, p.Balance-p.Held, p.GuaranteeFloor, p.City,
		p.CreatedAt, p.UpdatedAt)
}

// viewToDomain converts a payments_ext.agent_profiles row.
func viewToDomain(p sqlc.PaymentsExtAgentProfile) (agentprofile.AgentProfile, error) {
	return build(p.ID, p.AccountNk, p.Status, p.Level, p.Currency,
		p.Balance, p.Held, p.Available, p.GuaranteeFloor, p.City,
		p.CreatedAt, p.UpdatedAt)
}

func build(
	id, accountNk string, status sqlc.AgentProfileStatus, level sqlc.AgentLevel,
	currency string, balance, held, available, floor int64, city pgtype.Text,
	createdAt, updatedAt time.Time,
) (agentprofile.AgentProfile, error) {
	pid, err := uuid.Parse(id)
	if err != nil {
		return agentprofile.AgentProfile{}, fmt.Errorf("parsing agent profile id %q: %w", id, err)
	}
	account, err := uuid.Parse(accountNk)
	if err != nil {
		return agentprofile.AgentProfile{}, fmt.Errorf("parsing account id %q: %w", accountNk, err)
	}

	out := agentprofile.AgentProfile{
		ID:             pid,
		AccountID:      account,
		Status:         agentprofile.Status(status),
		Level:          agentprofile.Level(level),
		Currency:       currency,
		Balance:        balance,
		Held:           held,
		Available:      available,
		GuaranteeFloor: floor,
		CreatedAt:      createdAt,
		UpdatedAt:      updatedAt,
	}
	if city.Valid {
		out.City = new(city.String)
	}

	return out, nil
}

func toText(s *string) pgtype.Text {
	if s == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *s, Valid: true}
}
