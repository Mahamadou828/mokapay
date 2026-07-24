package internal

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	migrate "github.com/rubenv/sql-migrate"
	filedb "mokapay.app/app/services/file/internal/db"
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
		return pg.ExecFolderWithTX(ctx, tx, filedb.Scripts)
	}); err != nil {
		err = fmt.Errorf("applying scripts: %w", err)
		log.Error(ctx, "migrate", "error", err)
		return err
	}

	migrations := &migrate.EmbedFileSystemMigrationSource{FileSystem: filedb.Migrations, Root: "migrations"}
	n, err := migrate.Exec(pg.Pool, "postgres", migrations, migrate.Up)
	if err != nil {
		err = fmt.Errorf("applying migrations: %w", err)
		log.Error(ctx, "migrate", "error", err)
		return err
	}

	if err := pg.Begin(ctx, func(tx *sql.Tx) error {
		if err := pg.ExecFolderWithTX(ctx, tx, filedb.Views); err != nil {
			return err
		}
		return pg.ExecFolderWithTX(ctx, tx, filedb.Procedures)
	}); err != nil {
		err = fmt.Errorf("applying views and procedures: %w", err)
		log.Error(ctx, "migrate", "error", err)
		return err
	}

	log.Info(ctx, "migrate", "status", "complete", "migrations", n)
	return nil
}
