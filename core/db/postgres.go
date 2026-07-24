package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"

	// pgx in database/sql-compatibility mode: repos and sqlc stay on
	// database/sql; pgx-native consumers (River) share the same DSN.
	_ "github.com/jackc/pgx/v5/stdlib"
)

type PostgresConfig struct {
	Host     string
	Port     string
	Username string
	Password string
	Database string
}

type Postgres struct {
	Pool   *sql.DB
	config PostgresConfig
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

	db, err := sql.Open("pgx", conn)
	if err != nil {
		return err
	}

	if err := db.Ping(); err != nil {
		return err
	}
	d.Pool = db
	return nil
}

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
	return d.Pool.Close()
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
