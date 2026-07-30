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

	// Extend as the compliance schema grows.
	statements := []string{
		"DROP SCHEMA IF EXISTS compliance_ext CASCADE",
		"DROP TABLE IF EXISTS case_alerts",
		"DROP TABLE IF EXISTS cases",
		"DROP SEQUENCE IF EXISTS case_ref_seq",
		"DROP TABLE IF EXISTS alerts",
		"DROP TABLE IF EXISTS rule_evaluations",
		"DROP TYPE IF EXISTS compliance_case_status",
		"DROP TYPE IF EXISTS compliance_alert_status",
		"DROP TYPE IF EXISTS compliance_severity",
		"DROP TYPE IF EXISTS compliance_rule_code",
		"DROP TYPE IF EXISTS compliance_subject_type",
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
