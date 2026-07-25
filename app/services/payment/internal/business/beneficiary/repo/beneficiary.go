// Package repo owns the beneficiary persistence.
package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"mokapay.app/app/services/payment/internal/business/beneficiary"
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

// Add upserts on the live (client, phone) pair — re-sending the same
// recipient refreshes it. clientID is an explicit parameter on purpose.
func (s Store) Add(ctx context.Context, clientID uuid.UUID, nb beneficiary.NewBeneficiary) (beneficiary.Beneficiary, error) {
	row, err := s.sqlc.AddBeneficiary(ctx, sqlc.AddBeneficiaryParams{
		ClientNk: clientID.String(),
		Name:     nb.Name,
		Phone:    nb.Phone,
		Operator: sqlc.PaymentOperator(nb.Operator),
		City:     toText(nb.City),
	})
	if err != nil {
		return beneficiary.Beneficiary{}, fmt.Errorf("adding beneficiary: %w", err)
	}

	return build(row.ID, row.ClientNk, row.Name, row.Phone, row.Operator, row.City, row.CreatedAt, row.UpdatedAt)
}

// Get returns one beneficiary.
func (s Store) Get(ctx context.Context, id string) (beneficiary.Beneficiary, error) {
	row, err := s.sqlc.GetBeneficiary(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return beneficiary.Beneficiary{}, beneficiary.ErrBeneficiaryNotFound
		}
		return beneficiary.Beneficiary{}, fmt.Errorf("getting beneficiary %s: %w", id, err)
	}

	return build(row.ID, row.ClientNk, row.Name, row.Phone, row.Operator, row.City, row.CreatedAt, row.UpdatedAt)
}

// List returns the client's saved recipients, newest first.
func (s Store) List(ctx context.Context, clientID uuid.UUID) ([]beneficiary.Beneficiary, error) {
	rows, err := s.sqlc.ListBeneficiaries(ctx, clientID.String())
	if err != nil {
		return nil, fmt.Errorf("listing beneficiaries: %w", err)
	}

	out := make([]beneficiary.Beneficiary, 0, len(rows))
	for _, row := range rows {
		b, err := build(row.ID, row.ClientNk, row.Name, row.Phone, row.Operator, row.City, row.CreatedAt, row.UpdatedAt)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}

	return out, nil
}

// Delete soft-deletes, owner-scoped; deleting a foreign or missing row
// is a silent no-op at the SQL level (the view simply stops listing it).
func (s Store) Delete(ctx context.Context, id string, clientID uuid.UUID) error {
	if err := s.sqlc.DeleteBeneficiary(ctx, sqlc.DeleteBeneficiaryParams{
		ID:       id,
		ClientNk: clientID.String(),
	}); err != nil {
		return fmt.Errorf("deleting beneficiary %s: %w", id, err)
	}
	return nil
}

func build(id, clientNk, name, phone string, operator sqlc.PaymentOperator, city pgtype.Text, createdAt, updatedAt time.Time) (beneficiary.Beneficiary, error) {
	bid, err := uuid.Parse(id)
	if err != nil {
		return beneficiary.Beneficiary{}, fmt.Errorf("parsing beneficiary id %q: %w", id, err)
	}
	client, err := uuid.Parse(clientNk)
	if err != nil {
		return beneficiary.Beneficiary{}, fmt.Errorf("parsing client id %q: %w", clientNk, err)
	}

	out := beneficiary.Beneficiary{
		ID:        bid,
		ClientID:  client,
		Name:      name,
		Phone:     phone,
		Operator:  transfer.Operator(operator),
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
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
