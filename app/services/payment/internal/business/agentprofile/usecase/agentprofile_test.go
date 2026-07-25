package usecase_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"mokapay.app/app/services/payment/internal/business/agentprofile"
	"mokapay.app/app/services/payment/internal/business/agentprofile/usecase"
	coredb "mokapay.app/core/db"
	"mokapay.app/core/logger"
)

// Integration test: needs a migrated payments database. Skipped unless
// TEST_DB_PORT is set:
//
//	TEST_DB_PORT=5433 go test ./internal/business/agentprofile/...
func newTestUseCase(t *testing.T) usecase.UseCase {
	t.Helper()

	port := os.Getenv("TEST_DB_PORT")
	if port == "" {
		t.Skip("TEST_DB_PORT not set; integration test needs a migrated payments DB")
	}

	db := coredb.New(coredb.PostgresConfig{
		Host:     getenv("TEST_DB_HOST", "localhost"),
		Port:     port,
		Username: getenv("TEST_DB_USER", "mokapay"),
		Password: getenv("TEST_DB_PASSWORD", "mokapay"),
		Database: getenv("TEST_DB_NAME", "payments"),
	})
	if err := db.Open(); err != nil {
		t.Fatalf("opening database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	return usecase.New(logger.New(os.Stderr, logger.LevelInfo, "test", nil), db)
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func TestAgentProfileLifecycle(t *testing.T) {
	uc := newTestUseCase(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	account := uuid.New()
	p, err := uc.Create(ctx, agentprofile.NewAgentProfile{AccountID: account})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if p.Status != agentprofile.StatusPending {
		t.Fatalf("new profile status = %q", p.Status)
	}

	// Idempotent creators get AlreadyExists and look it up.
	if _, err := uc.Create(ctx, agentprofile.NewAgentProfile{AccountID: account}); !errors.Is(err, agentprofile.ErrProfileExists) {
		t.Errorf("re-create error = %v, want ErrProfileExists", err)
	}
	if got, err := uc.GetByAccount(ctx, account); err != nil || got.ID != p.ID {
		t.Fatalf("GetByAccount = %+v, %v", got, err)
	}

	// Suspending a pending profile is illegal.
	if _, err := uc.Suspend(ctx, p.ID); !errors.Is(err, agentprofile.ErrInvalidTransition) {
		t.Errorf("suspend-pending error = %v, want ErrInvalidTransition", err)
	}

	// pending → active, and activating again is a no-op success (the
	// enrollment-approve retry).
	active, err := uc.Activate(ctx, p.ID)
	if err != nil || active.Status != agentprofile.StatusActive {
		t.Fatalf("Activate = %+v, %v", active, err)
	}
	again, err := uc.Activate(ctx, p.ID)
	if err != nil || again.Status != agentprofile.StatusActive {
		t.Fatalf("re-Activate = %+v, %v — must be a no-op success", again, err)
	}

	// active → suspended → active.
	suspended, err := uc.Suspend(ctx, p.ID)
	if err != nil || suspended.Status != agentprofile.StatusSuspended {
		t.Fatalf("Suspend = %+v, %v", suspended, err)
	}
	if _, err := uc.Activate(ctx, p.ID); err != nil {
		t.Fatalf("re-Activate from suspended: %v", err)
	}

	// Zero-money profile deletes fine; a second delete is NotFound.
	if err := uc.Delete(ctx, p.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := uc.Delete(ctx, p.ID); !errors.Is(err, agentprofile.ErrProfileNotFound) {
		t.Errorf("re-Delete error = %v, want ErrProfileNotFound", err)
	}

	// The account is free for a fresh profile after deletion.
	if _, err := uc.Create(ctx, agentprofile.NewAgentProfile{AccountID: account}); err != nil {
		t.Errorf("create after delete: %v", err)
	}
}
