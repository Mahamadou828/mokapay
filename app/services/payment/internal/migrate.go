package internal

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
	migrate "github.com/rubenv/sql-migrate"
	paymentdb "mokapay.app/app/services/payment/internal/db"
	"mokapay.app/core/db"
)

// Migrate applies the service's SQL in the workspace order:
// scripts (one tx) → versioned migrations (sql-migrate) → views + procedures (one tx).
func Migrate() error {
	ctx := context.Background()
	cfg := newConfig()
	log := newLogger()

	pg := db.New(cfg.DB)
	if err := pg.Open(); err != nil {
		log.Error(ctx, "migrate", "failed to open database", "error", err)
		return err
	}
	defer pg.Close()

	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	if err := pg.Begin(ctx, func(tx *sql.Tx) error {
		return pg.ExecFolderWithTX(ctx, tx, paymentdb.Scripts)
	}); err != nil {
		err = fmt.Errorf("applying scripts: %w", err)
		log.Error(ctx, "migrate", "error", err)
		return err
	}

	migrations := &migrate.EmbedFileSystemMigrationSource{FileSystem: paymentdb.Migrations, Root: "migrations"}
	n, err := migrate.Exec(pg.Pool, "postgres", migrations, migrate.Up)
	if err != nil {
		err = fmt.Errorf("applying migrations: %w", err)
		log.Error(ctx, "migrate", "error", err)
		return err
	}

	// River owns its own schema (river_job, …) — applied idempotently on
	// every run, like our views/procedures.
	riverMigrator, err := rivermigrate.New(riverpgxv5.New(pg.PgxPool), nil)
	if err != nil {
		err = fmt.Errorf("initializing river migrator: %w", err)
		log.Error(ctx, "migrate", "error", err)
		return err
	}
	if _, err := riverMigrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		err = fmt.Errorf("applying river migrations: %w", err)
		log.Error(ctx, "migrate", "error", err)
		return err
	}

	if err := pg.Begin(ctx, func(tx *sql.Tx) error {
		if err := pg.ExecFolderWithTX(ctx, tx, paymentdb.Views); err != nil {
			return err
		}
		return pg.ExecFolderWithTX(ctx, tx, paymentdb.Procedures)
	}); err != nil {
		err = fmt.Errorf("applying views and procedures: %w", err)
		log.Error(ctx, "migrate", "error", err)
		return err
	}

	log.Info(ctx, "migrate", "status", "complete", "migrations", n)
	return nil
}
