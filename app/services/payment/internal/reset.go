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

	// Extend as the payment schema lands (tables, enums) — see the
	// payment-service design doc.
	statements := []string{
		"DROP SCHEMA IF EXISTS payments_ext CASCADE",
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
