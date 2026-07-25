package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
)

type PostgresConfig struct {
	Host     string
	Port     string
	Username string
	Password string
	Database string
}

// Postgres owns a single pgx connection pool exposed through two interfaces:
// PgxPool for pgx-native consumers (River) and Pool, a database/sql facade
// over the same pool, for repos and sqlc.
type Postgres struct {
	Pool    *sql.DB
	PgxPool *pgxpool.Pool
	config  PostgresConfig
}

func New(config PostgresConfig) *Postgres {
	return &Postgres{config: config}
}

func (d *Postgres) Open() error {
	conn := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		d.config.Host,
		d.config.Port,
		d.config.Username,
		d.config.Password,
		d.config.Database,
	)

	// The pool connects lazily; Open keeps its no-context signature and the
	// Ping below is what actually reaches the server.
	pool, err := pgxpool.New(context.Background(), conn)
	if err != nil {
		return err
	}

	db := stdlib.OpenDBFromPool(pool)
	if err := db.Ping(); err != nil {
		_ = db.Close() // best-effort teardown of the facade before the pool
		pool.Close()
		return err
	}

	d.Pool, d.PgxPool = db, pool
	return nil
}

// BeginPgx runs f inside a pgx-native transaction — the repo/usecase path
// (sqlc pgx/v5, River InsertTx). Commit on nil, rollback on error or panic.
func (d *Postgres) BeginPgx(ctx context.Context, f func(tx pgx.Tx) error) error {
	return pgx.BeginFunc(ctx, d.PgxPool, f)
}

// Begin is the database/sql twin, kept for the migration path
// (ExecFolderWithTX over the stdlib facade).
func (d *Postgres) Begin(ctx context.Context, f func(tx *sql.Tx) error) error {
	tx, err := d.Pool.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := f(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (d *Postgres) Close() error {
	// The database/sql facade must be closed before the pgx pool it borrows
	// its connections from.
	err := d.Pool.Close()
	d.PgxPool.Close()
	return err
}

func (d *Postgres) ExecContextWithTX() {

}

func (d *Postgres) ExecFolderWithTX(ctx context.Context, tx *sql.Tx, folder embed.FS) error {
	if err := fs.WalkDir(folder, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		b, err := folder.ReadFile(path)
		if err != nil {
			return err
		}

		if _, err := tx.ExecContext(ctx, string(b)); err != nil {
			return err
		}
		return nil
	}); err != nil {
		return err
	}
	return nil
}
