package internal

import (
	"context"
	"fmt"
	"time"

	"mokapay.app/core/db"
)

// Reset drops every database object the service owns, including the
// sql-migrate bookkeeping table. Follow with `migrate` (and `seed`) to rebuild.
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

	statements := []string{
		"DROP SCHEMA IF EXISTS users_ext CASCADE",
		"DROP TABLE IF EXISTS refresh_tokens CASCADE",
		"DROP TABLE IF EXISTS otps CASCADE",
		"DROP TABLE IF EXISTS accounts CASCADE",
		"DROP TABLE IF EXISTS gorp_migrations",
		"DROP TYPE IF EXISTS otp_purpose",
		"DROP TYPE IF EXISTS account_language",
		"DROP TYPE IF EXISTS account_status",
		"DROP TYPE IF EXISTS account_role",
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
