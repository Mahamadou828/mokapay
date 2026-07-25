package internal

import (
	"context"
	"fmt"
	"time"

	"mokapay.app/core/db"
)

// Reset drops every database object the service owns, including the
// sql-migrate bookkeeping table. Follow with `migrate` to rebuild.
func Reset() error {
	ctx := context.Background()
	cfg := newConfig()
	log := newLogger()

	pg := db.New(cfg.DB)
	if err := pg.Open(); err != nil {
		log.Error(ctx, "reset", "failed to open database", "error", err)
		return err
	}
	defer pg.Close()

	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	// Extend as the payment schema grows (transfers, offers, holds, …).
	statements := []string{
		"DROP SCHEMA IF EXISTS payments_ext CASCADE",
		"DROP TABLE IF EXISTS ledger_lines",
		"DROP TABLE IF EXISTS ledger_requests",
		"DROP TABLE IF EXISTS agent_profiles",
		"DROP TYPE IF EXISTS ledger_direction",
		"DROP TYPE IF EXISTS ledger_request_status",
		"DROP TYPE IF EXISTS ledger_request_type",
		"DROP TYPE IF EXISTS agent_level",
		"DROP TYPE IF EXISTS agent_profile_status",
		"DROP TABLE IF EXISTS gorp_migrations",
		"DROP FUNCTION IF EXISTS set_updated_at",
	}
	for _, stmt := range statements {
		if _, err := pg.Pool.ExecContext(ctx, stmt); err != nil {
			err = fmt.Errorf("executing %q: %w", stmt, err)
			log.Error(ctx, "reset", "error", err)
			return err
		}
	}

	log.Info(ctx, "reset", "status", "complete")
	return nil
}
