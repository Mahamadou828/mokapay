package internal

import (
	"context"
	"errors"
	"fmt"
	"time"

	"mokapay.app/core/db"
)

// Seed provisions the bootstrap back-office account (spec: staff accounts
// are never self-registered). Role admin, not ops: staff creation and role
// changes are admin-only, so the first account must be able to mint the
// rest. Idempotent: a live account on the seed phone is left as-is.
func Seed() error {
	ctx := context.Background()
	cfg := newConfig()
	log := newLogger()

	if cfg.Seed.OpsPhone == "" {
		err := errors.New("SEED_OPS_PHONE must be set")
		log.Error(ctx, "seed", "error", err)
		return err
	}

	pg := db.New(cfg.DB)
	if err := pg.Open(); err != nil {
		log.Error(ctx, "seed", "failed to open database", "error", err)
		return err
	}
	defer pg.Close()

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var exists bool
	if err := pg.Pool.QueryRowContext(ctx,
		"SELECT EXISTS(SELECT 1 FROM accounts WHERE phone_number = $1 AND deleted_at IS NULL)",
		cfg.Seed.OpsPhone,
	).Scan(&exists); err != nil {
		err = fmt.Errorf("checking for existing ops account: %w", err)
		log.Error(ctx, "seed", "error", err)
		return err
	}
	if exists {
		log.Info(ctx, "seed", "status", "ops account already present", "phone", cfg.Seed.OpsPhone)
		return nil
	}

	// Credentials live in Cognito: seed only creates our account row. The
	// matching Cognito user (same phone as username) is provisioned separately.
	if _, err := pg.Pool.ExecContext(ctx,
		"SELECT users_ext.add_account($1, $2, $3, NULL, 'fr', 'admin', 'active')",
		cfg.Seed.OpsPhone, "Moka", "Ops",
	); err != nil {
		err = fmt.Errorf("inserting ops account: %w", err)
		log.Error(ctx, "seed", "error", err)
		return err
	}

	log.Info(ctx, "seed", "status", "ops account created", "phone", cfg.Seed.OpsPhone)
	return nil
}
