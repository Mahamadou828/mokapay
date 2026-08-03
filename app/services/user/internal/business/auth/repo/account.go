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
	"mokapay.app/app/services/user/internal/business/auth"
	"mokapay.app/app/services/user/internal/sqlc"
)

// AddAccount creates the account row for a verified registration. Role and
// status are left to the store defaults (user/active); Code and Password on
// the payload are auth-flow fields and never reach the database. An empty
// sub is stored as NULL (unprovisioned) so it never trips the sub unique index.
func (s Store) AddAccount(ctx context.Context, na auth.NewAccount, sub string) (auth.Account, error) {
	var subParam pgtype.Text
	if sub != "" {
		subParam = pgtype.Text{String: sub, Valid: true}
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

// AddStaffAccount creates a back-office account row. Role and status are
// explicit parameters on purpose — a provisioned staffer never lands on
// the store defaults, and silently deriving them on the payload would let
// a caller forget they exist.
func (s Store) AddStaffAccount(ctx context.Context, role auth.AccountRole, status auth.AccountStatus, na auth.NewAccount, sub string) (auth.Account, error) {
	var subParam pgtype.Text
	if sub != "" {
		subParam = pgtype.Text{String: sub, Valid: true}
	}

	row, err := s.sqlc.AddAccount(ctx, sqlc.AddAccountParams{
		PhoneNumber:       na.PhoneNumber,
		CognitoSub:        subParam,
		FirstName:         na.FirstName,
		LastName:          na.LastName,
		Email:             toNullString(na.Email),
		PreferredLanguage: toNullLanguage(na.PreferredLanguage),
		Role:              sqlc.NullAccountRole{AccountRole: sqlc.AccountRole(role), Valid: true},
		Status:            sqlc.NullAccountStatus{AccountStatus: sqlc.AccountStatus(status), Valid: true},
	})
	if err != nil {
		if conflict := uniqueViolationErr(err); conflict != nil {
			return auth.Account{}, conflict
		}
		return auth.Account{}, fmt.Errorf("adding staff account: %w", err)
	}

	return accountToDomain(row)
}

// GetAccountBySub returns the live account bound to the Cognito user
// (unique among live provisioned rows).
func (s Store) GetAccountBySub(ctx context.Context, cognitoSub string) (auth.Account, error) {
	row, err := s.sqlc.GetAccountBySub(ctx, pgtype.Text{String: cognitoSub, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
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
		if errors.Is(err, pgx.ErrNoRows) {
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
		if errors.Is(err, pgx.ErrNoRows) {
			return auth.Account{}, auth.ErrAccountNotFound
		}
		return auth.Account{}, fmt.Errorf("getting account by phone: %w", err)
	}

	return viewAccountToDomain(row)
}

// UpdateAccount writes the non-nil fields of the payload; nil leaves the
// stored value untouched.
func (s Store) UpdateAccount(ctx context.Context, up auth.UpdateAccount) (auth.Account, error) {
	row, err := s.sqlc.UpdateAccount(ctx, sqlc.UpdateAccountParams{
		ID:                 up.ID.String(),
		FirstName:          toNullString(up.FirstName),
		LastName:           toNullString(up.LastName),
		Email:              toNullString(up.Email),
		PreferredLanguage:  toNullLanguagePtr(up.PreferredLanguage),
		PreferredOtpMethod: toNullOtpMethodPtr(up.PreferredOtpMethod),
		Role:               toNullRole(up.Role),
		Status:             toNullStatus(up.Status),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return auth.Account{}, auth.ErrAccountNotFound
		}
		return auth.Account{}, fmt.Errorf("updating account %s: %w", up.ID, err)
	}

	return accountToDomain(row)
}

// ListAccounts returns live accounts matching the filter, newest first.
func (s Store) ListAccounts(ctx context.Context, f auth.AccountFilter) ([]auth.Account, error) {
	rows, err := s.sqlc.ListAccounts(ctx, sqlc.ListAccountsParams{
		Role:        toNullRole(f.Role),
		Status:      toNullStatus(f.Status),
		PhoneNumber: toNullString(f.PhoneNumber),
	})
	if err != nil {
		return nil, fmt.Errorf("listing accounts: %w", err)
	}

	out := make([]auth.Account, 0, len(rows))
	for _, row := range rows {
		a, err := viewAccountToDomain(row)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}

	return out, nil
}

// DeleteAccount soft-deletes the account; deleting an already-deleted or
// unknown id is a no-op.
func (s Store) DeleteAccount(ctx context.Context, id string) error {
	if err := s.sqlc.DeleteAccount(ctx, id); err != nil {
		return fmt.Errorf("deleting account %s: %w", id, err)
	}
	return nil
}

// SetLastLogin stamps a successful login on the account.
func (s Store) SetLastLogin(ctx context.Context, id string, at time.Time) (auth.Account, error) {
	row, err := s.sqlc.UpdateAccount(ctx, sqlc.UpdateAccountParams{
		ID:          id,
		LastLoginAt: pgtype.Timestamptz{Time: at, Valid: true},
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
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
		ID:                 id,
		PhoneNumber:        a.PhoneNumber,
		FirstName:          a.FirstName,
		LastName:           a.LastName,
		PreferredLanguage:  auth.Language(a.PreferredLanguage),
		PreferredOtpMethod: auth.OtpMethod(a.PreferredOtpMethod),
		Role:               auth.AccountRole(a.Role),
		Status:             auth.AccountStatus(a.Status),
		CreatedAt:          a.CreatedAt,
		UpdatedAt:          a.UpdatedAt,
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
		ID:                 id,
		PhoneNumber:        a.PhoneNumber,
		FirstName:          a.FirstName,
		LastName:           a.LastName,
		PreferredLanguage:  auth.Language(a.PreferredLanguage),
		PreferredOtpMethod: auth.OtpMethod(a.PreferredOtpMethod),
		Role:               auth.AccountRole(a.Role),
		Status:             auth.AccountStatus(a.Status),
		CreatedAt:          a.CreatedAt,
		UpdatedAt:          a.UpdatedAt,
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
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		return nil
	}

	switch pgErr.ConstraintName {
	case "accounts_phone_number_uq":
		return auth.ErrPhoneNumberTaken
	case "accounts_cognito_sub_uq":
		return auth.ErrCognitoSubTaken
	}
	return nil
}

func toNullString(s *string) pgtype.Text {
	if s == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *s, Valid: true}
}

func toNullLanguage(l auth.Language) sqlc.NullAccountLanguage {
	if l == "" {
		return sqlc.NullAccountLanguage{}
	}
	return sqlc.NullAccountLanguage{AccountLanguage: sqlc.AccountLanguage(l), Valid: true}
}

func toNullLanguagePtr(l *auth.Language) sqlc.NullAccountLanguage {
	if l == nil {
		return sqlc.NullAccountLanguage{}
	}
	return toNullLanguage(*l)
}

func toNullOtpMethodPtr(m *auth.OtpMethod) sqlc.NullOtpMethod {
	if m == nil {
		return sqlc.NullOtpMethod{}
	}
	return sqlc.NullOtpMethod{OtpMethod: sqlc.OtpMethod(*m), Valid: true}
}

func toNullRole(r *auth.AccountRole) sqlc.NullAccountRole {
	if r == nil {
		return sqlc.NullAccountRole{}
	}
	return sqlc.NullAccountRole{AccountRole: sqlc.AccountRole(*r), Valid: true}
}

func toNullStatus(st *auth.AccountStatus) sqlc.NullAccountStatus {
	if st == nil {
		return sqlc.NullAccountStatus{}
	}
	return sqlc.NullAccountStatus{AccountStatus: sqlc.AccountStatus(*st), Valid: true}
}
