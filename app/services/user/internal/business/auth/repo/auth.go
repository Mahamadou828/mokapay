// Package repo owns the auth domain's persistence: the otps table.
// Credentials and sessions live in Cognito — never here. Reads go through
// the users_ext views, writes through the users_ext procedures.
package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"mokapay.app/app/services/user/internal/business/auth"
	"mokapay.app/app/services/user/internal/sqlc"
	coredb "mokapay.app/core/db"
)

type Store struct {
	sqlc *sqlc.Queries
}

func New(db *coredb.Postgres) Store {
	return Store{sqlc: sqlc.New(db.PgxPool)}
}

// WithTx returns a Store running on the given transaction — used by the
// enrollment usecase to flip an account role atomically with an
// enrollment transition.
func (s Store) WithTx(tx pgx.Tx) Store {
	return Store{sqlc: s.sqlc.WithTx(tx)}
}

// GetValidOtp returns the newest live (unconsumed, unexpired) code for the
// phone/purpose pair.
func (s Store) GetValidOtp(ctx context.Context, phoneNumber string, purpose auth.OtpPurpose) (auth.Otp, error) {
	row, err := s.sqlc.GetValidOtp(ctx, sqlc.GetValidOtpParams{
		PhoneNumber: phoneNumber,
		Purpose:     sqlc.OtpPurpose(purpose),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return auth.Otp{}, auth.ErrOtpNotFound
		}
		return auth.Otp{}, fmt.Errorf("getting valid otp: %w", err)
	}

	return viewOtpToDomain(row)
}

// AddOtp stores a freshly issued code (already hashed by the usecase).
func (s Store) AddOtp(ctx context.Context, no auth.NewOtp) (auth.Otp, error) {
	row, err := s.sqlc.AddOtp(ctx, sqlc.AddOtpParams{
		PhoneNumber: no.PhoneNumber,
		CodeHash:    no.CodeHash,
		Purpose:     sqlc.OtpPurpose(no.Purpose),
		ExpiresAt:   no.ExpiresAt,
	})
	if err != nil {
		return auth.Otp{}, fmt.Errorf("adding otp: %w", err)
	}

	return otpToDomain(row)
}

// ConsumeOtp atomically marks the code used. auth.ErrOtpNotFound means it was
// already consumed, expired, or a parallel verify won the race — reject the
// attempt in every case.
func (s Store) ConsumeOtp(ctx context.Context, id string) (auth.Otp, error) {
	row, err := s.sqlc.ConsumeOtp(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return auth.Otp{}, auth.ErrOtpNotFound
		}
		return auth.Otp{}, fmt.Errorf("consuming otp %s: %w", id, err)
	}

	return otpToDomain(row)
}

// RefreshOtp swaps in a newly issued code hash and extends the expiry on a
// still-live code (the resend path). The resend is counted against attempts,
// so the caller's attempts cap limits resend spam too. auth.ErrOtpNotFound
// means the code was consumed or deleted meanwhile — issue a fresh one instead.
func (s Store) RefreshOtp(ctx context.Context, id, codeHash string, expiresAt time.Time) (auth.Otp, error) {
	row, err := s.sqlc.RefreshOtp(ctx, sqlc.RefreshOtpParams{
		ID:        id,
		CodeHash:  codeHash,
		ExpiresAt: expiresAt,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return auth.Otp{}, auth.ErrOtpNotFound
		}
		return auth.Otp{}, fmt.Errorf("refreshing otp %s: %w", id, err)
	}

	return otpToDomain(row)
}

// BumpOtpAttempts counts a failed verify against the code.
func (s Store) BumpOtpAttempts(ctx context.Context, id string) (auth.Otp, error) {
	row, err := s.sqlc.BumpOtpAttempts(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return auth.Otp{}, auth.ErrOtpNotFound
		}
		return auth.Otp{}, fmt.Errorf("bumping otp attempts %s: %w", id, err)
	}

	return otpToDomain(row)
}

// otpToDomain converts a base-table row (write procedures return SETOF otps).
func otpToDomain(o sqlc.Otp) (auth.Otp, error) {
	id, err := uuid.Parse(o.ID)
	if err != nil {
		return auth.Otp{}, fmt.Errorf("parsing otp id %q: %w", o.ID, err)
	}

	out := auth.Otp{
		ID:          id,
		PhoneNumber: o.PhoneNumber,
		CodeHash:    o.CodeHash,
		Purpose:     auth.OtpPurpose(o.Purpose),
		Attempts:    int(o.Attempts),
		ExpiresAt:   o.ExpiresAt,
		CreatedAt:   o.CreatedAt,
		UpdatedAt:   o.UpdatedAt,
	}
	if o.ConsumedAt.Valid {
		out.ConsumedAt = new(o.ConsumedAt.Time)
	}

	return out, nil
}

// viewOtpToDomain converts a users_ext.otps row; view rows are live by
// definition, so ConsumedAt stays nil.
func viewOtpToDomain(o sqlc.UsersExtOtp) (auth.Otp, error) {
	id, err := uuid.Parse(o.ID)
	if err != nil {
		return auth.Otp{}, fmt.Errorf("parsing otp id %q: %w", o.ID, err)
	}

	return auth.Otp{
		ID:          id,
		PhoneNumber: o.PhoneNumber,
		CodeHash:    o.CodeHash,
		Purpose:     auth.OtpPurpose(o.Purpose),
		Attempts:    int(o.Attempts),
		ExpiresAt:   o.ExpiresAt,
		CreatedAt:   o.CreatedAt,
		UpdatedAt:   o.UpdatedAt,
	}, nil
}
