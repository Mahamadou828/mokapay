package repo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"mokapay.app/app/services/user/internal/business/auth"
	"mokapay.app/app/services/user/internal/sqlc"
)

// AddAccount creates the account row for a verified registration. Role and
// status are left to the store defaults (user/active); Code and Password on
// the payload are auth-flow fields and never reach the database. An empty
// sub is stored as NULL (unprovisioned) so it never trips the sub unique index.
func (s Store) AddAccount(ctx context.Context, na auth.NewAccount, sub string) (auth.Account, error) {
	var subParam sql.NullString
	if sub != "" {
		subParam = sql.NullString{String: sub, Valid: true}
	}

	row, err := s.sqlc.AddAccount(ctx, sqlc.AddAccountParams{
		PhoneNumber:       na.PhoneNumber,
		CognitoSub:        subParam,
		FirstName:         na.FirstName,
		LastName:          na.LastName,
		Email:             toNullString(na.Email),
		PreferredLanguage: toNullLanguage(na.PreferredLanguage),
	})
	if err != nil {
		if conflict := uniqueViolationErr(err); conflict != nil {
			return auth.Account{}, conflict
		}
		return auth.Account{}, fmt.Errorf("adding account: %w", err)
	}

	return accountToDomain(row)
}

// GetAccountBySub returns the live account bound to the Cognito user
// (unique among live provisioned rows).
func (s Store) GetAccountBySub(ctx context.Context, cognitoSub string) (auth.Account, error) {
	row, err := s.sqlc.GetAccountBySub(ctx, sql.NullString{String: cognitoSub, Valid: true})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return auth.Account{}, auth.ErrAccountNotFound
		}
		return auth.Account{}, fmt.Errorf("getting account by sub: %w", err)
	}

	return viewAccountToDomain(row)
}

// GetAccount returns the live account with the given id.
func (s Store) GetAccount(ctx context.Context, id string) (auth.Account, error) {
	row, err := s.sqlc.GetAccount(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return auth.Account{}, auth.ErrAccountNotFound
		}
		return auth.Account{}, fmt.Errorf("getting account %s: %w", id, err)
	}

	return viewAccountToDomain(row)
}

// GetAccountByPhone returns the live account owning the phone number
// (unique among live rows — phone is the login identity).
func (s Store) GetAccountByPhone(ctx context.Context, phoneNumber string) (auth.Account, error) {
	row, err := s.sqlc.GetAccountByPhone(ctx, phoneNumber)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return auth.Account{}, auth.ErrAccountNotFound
		}
		return auth.Account{}, fmt.Errorf("getting account by phone: %w", err)
	}

	return viewAccountToDomain(row)
}

// SetLastLogin stamps a successful login on the account.
func (s Store) SetLastLogin(ctx context.Context, id string, at time.Time) (auth.Account, error) {
	row, err := s.sqlc.UpdateAccount(ctx, sqlc.UpdateAccountParams{
		ID:          id,
		LastLoginAt: sql.NullTime{Time: at, Valid: true},
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return auth.Account{}, auth.ErrAccountNotFound
		}
		return auth.Account{}, fmt.Errorf("setting last login on account %s: %w", id, err)
	}

	return accountToDomain(row)
}

// accountToDomain converts a base-table row (write procedures return accounts).
func accountToDomain(a sqlc.Account) (auth.Account, error) {
	id, err := uuid.Parse(a.ID)
	if err != nil {
		return auth.Account{}, fmt.Errorf("parsing account id %q: %w", a.ID, err)
	}

	out := auth.Account{
		ID:                id,
		PhoneNumber:       a.PhoneNumber,
		FirstName:         a.FirstName,
		LastName:          a.LastName,
		PreferredLanguage: auth.Language(a.PreferredLanguage),
		Role:              auth.AccountRole(a.Role),
		Status:            auth.AccountStatus(a.Status),
		CreatedAt:         a.CreatedAt,
		UpdatedAt:         a.UpdatedAt,
	}
	if a.CognitoSub.Valid {
		out.CognitoSub = new(a.CognitoSub.String)
	}
	if a.Email.Valid {
		out.Email = new(a.Email.String)
	}
	if a.LastLoginAt.Valid {
		out.LastLoginAt = new(a.LastLoginAt.Time)
	}

	return out, nil
}

// viewAccountToDomain converts a users_ext.accounts row.
func viewAccountToDomain(a sqlc.UsersExtAccount) (auth.Account, error) {
	id, err := uuid.Parse(a.ID)
	if err != nil {
		return auth.Account{}, fmt.Errorf("parsing account id %q: %w", a.ID, err)
	}

	out := auth.Account{
		ID:                id,
		PhoneNumber:       a.PhoneNumber,
		FirstName:         a.FirstName,
		LastName:          a.LastName,
		PreferredLanguage: auth.Language(a.PreferredLanguage),
		Role:              auth.AccountRole(a.Role),
		Status:            auth.AccountStatus(a.Status),
		CreatedAt:         a.CreatedAt,
		UpdatedAt:         a.UpdatedAt,
	}
	if a.CognitoSub.Valid {
		out.CognitoSub = new(a.CognitoSub.String)
	}
	if a.Email.Valid {
		out.Email = new(a.Email.String)
	}
	if a.LastLoginAt.Valid {
		out.LastLoginAt = new(a.LastLoginAt.Time)
	}

	return out, nil
}

// uniqueViolationErr translates a Postgres unique_violation (23505) into the
// matching domain conflict, keyed on the violated index; nil for anything else.
func uniqueViolationErr(err error) error {
	var pqErr *pq.Error
	if !errors.As(err, &pqErr) || pqErr.Code != "23505" {
		return nil
	}

	switch pqErr.Constraint {
	case "accounts_phone_number_uq":
		return auth.ErrPhoneNumberTaken
	case "accounts_cognito_sub_uq":
		return auth.ErrCognitoSubTaken
	}
	return nil
}

func toNullString(s *string) sql.NullString {
	if s == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *s, Valid: true}
}

func toNullLanguage(l auth.Language) sqlc.NullAccountLanguage {
	if l == "" {
		return sqlc.NullAccountLanguage{}
	}
	return sqlc.NullAccountLanguage{AccountLanguage: sqlc.AccountLanguage(l), Valid: true}
}
