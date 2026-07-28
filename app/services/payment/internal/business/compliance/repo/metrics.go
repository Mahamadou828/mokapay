// Package repo owns the metrics persistence: three read-only aggregations
// over payments_ext.transfers, one per snapshot subject. Nothing here
// writes — the AML integration only ever reads payment's history.
package repo

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"mokapay.app/app/services/payment/internal/business/compliance"
	"mokapay.app/app/services/payment/internal/sqlc"
	coredb "mokapay.app/core/db"
)

type Store struct {
	sqlc *sqlc.Queries
}

func New(db *coredb.Postgres) Store {
	return Store{sqlc: sqlc.New(db.PgxPool)}
}

// SenderVolumes aggregates the transfers the client initiated. timezone is
// an IANA name (e.g. Africa/Kinshasa): the night-time count is a local
// clock question, and the database is the only place that knows every
// row's local hour without pulling the rows into Go.
func (s Store) SenderVolumes(ctx context.Context, clientID uuid.UUID, timezone string) (compliance.Volumes, error) {
	row, err := s.sqlc.GetSenderTransferMetrics(ctx, sqlc.GetSenderTransferMetricsParams{
		Timezone: timezone,
		ClientNk: clientID.String(),
	})
	if err != nil {
		return compliance.Volumes{}, fmt.Errorf("aggregating sender %s metrics: %w", clientID, err)
	}

	return compliance.Volumes{
		Volume24hMinor:   row.Volume24hMinor,
		Volume7dMinor:    row.Volume7dMinor,
		Count7d:          row.Count7d,
		Volume30dMinor:   row.Volume30dMinor,
		Count1h:          row.Count1h,
		Count7dNighttime: row.Count7dNighttime,
	}, nil
}

// AgentVolumes aggregates the transfers the agent profile served.
func (s Store) AgentVolumes(ctx context.Context, agentProfileID uuid.UUID, timezone string) (compliance.Volumes, error) {
	row, err := s.sqlc.GetAgentTransferMetrics(ctx, sqlc.GetAgentTransferMetricsParams{
		Timezone:       timezone,
		AgentProfileID: agentProfileID.String(),
	})
	if err != nil {
		return compliance.Volumes{}, fmt.Errorf("aggregating agent %s metrics: %w", agentProfileID, err)
	}

	return compliance.Volumes{
		Volume24hMinor:   row.Volume24hMinor,
		Volume7dMinor:    row.Volume7dMinor,
		Count7d:          row.Count7d,
		Volume30dMinor:   row.Volume30dMinor,
		Count1h:          row.Count1h,
		Count7dNighttime: row.Count7dNighttime,
	}, nil
}

// RecipientDistinctSenders counts how many distinct clients sent to the
// phone over the last seven days.
func (s Store) RecipientDistinctSenders(ctx context.Context, phone string) (int32, error) {
	count, err := s.sqlc.GetRecipientTransferMetrics(ctx, phone)
	if err != nil {
		return 0, fmt.Errorf("counting distinct senders to recipient: %w", err)
	}

	return count, nil
}
