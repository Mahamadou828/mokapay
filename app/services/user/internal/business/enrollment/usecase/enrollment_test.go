package usecase_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"mokapay.app/app/services/user/internal/business/auth"
	authrepo "mokapay.app/app/services/user/internal/business/auth/repo"
	"mokapay.app/app/services/user/internal/business/enrollment"
	"mokapay.app/app/services/user/internal/business/enrollment/usecase"
	"mokapay.app/app/services/user/internal/business/notification"
	coredb "mokapay.app/core/db"
	"mokapay.app/core/logger"
)

const tosVersion = "2026-07-25"

// fakeNotifier records the events pushed per account.
type fakeNotifier struct {
	events []string
}

func (f *fakeNotifier) Push(_ context.Context, nn notification.NewNotification) (notification.Notification, error) {
	f.events = append(f.events, nn.EventType)
	return notification.Notification{}, nil
}

// Integration tests: need a migrated users database (0003). Skipped unless
// TEST_DB_PORT is set:
//
//	TEST_DB_PORT=5433 go test ./internal/business/enrollment/usecase/
func newTestUseCase(t *testing.T) (usecase.UseCase, *fakeNotifier, authrepo.Store) {
	t.Helper()

	port := os.Getenv("TEST_DB_PORT")
	if port == "" {
		t.Skip("TEST_DB_PORT not set; integration test needs a migrated users DB")
	}

	db := coredb.New(coredb.PostgresConfig{
		Host:     getenv("TEST_DB_HOST", "localhost"),
		Port:     port,
		Username: getenv("TEST_DB_USER", "mokapay"),
		Password: getenv("TEST_DB_PASSWORD", "mokapay"),
		Database: getenv("TEST_DB_NAME", "users"),
	})
	if err := db.Open(); err != nil {
		t.Fatalf("opening database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	notifier := &fakeNotifier{}
	log := logger.New(os.Stderr, logger.LevelInfo, "test", nil)
	return usecase.New(log, db, notifier, tosVersion), notifier, authrepo.New(db)
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func testAccount(ctx context.Context, t *testing.T, accounts authrepo.Store, first string) uuid.UUID {
	t.Helper()

	phone := fmt.Sprintf("+2439%09d", time.Now().UnixNano()%1_000_000_000)
	acc, err := accounts.AddAccount(ctx, auth.NewAccount{
		PhoneNumber: phone,
		FirstName:   first,
		LastName:    "Usecase",
	}, "")
	if err != nil {
		t.Fatalf("creating test account: %v", err)
	}
	return acc.ID
}

func role(ctx context.Context, t *testing.T, accounts authrepo.Store, id uuid.UUID) auth.AccountRole {
	t.Helper()
	acc, err := accounts.GetAccount(ctx, id.String())
	if err != nil {
		t.Fatalf("reading account role: %v", err)
	}
	return acc.Role
}

func TestEnrollmentUseCaseFullFlow(t *testing.T) {
	uc, notifier, accounts := newTestUseCase(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	candidate := testAccount(ctx, t, accounts, "Candidate")
	admin := testAccount(ctx, t, accounts, "Admin")

	// Invite: enrollment created, role flipped, candidate notified.
	invited, err := uc.Invite(ctx, candidate, admin)
	if err != nil {
		t.Fatalf("Invite: %v", err)
	}
	if got := role(ctx, t, accounts, candidate); got != auth.AccountRolePendingAgent {
		t.Fatalf("role after invite = %q, want pending_agent", got)
	}
	if len(notifier.events) != 1 || notifier.events[0] != "enrollment.invited" {
		t.Errorf("events after invite = %v", notifier.events)
	}

	// A pending_agent is not eligible for a second invite (role guard, not
	// just the unique index).
	if _, err := uc.Invite(ctx, candidate, admin); !errors.Is(err, enrollment.ErrNotEligible) {
		t.Errorf("re-invite error = %v, want ErrNotEligible", err)
	}

	// Accept: stale TOS rejected, current one stamps.
	if _, err := uc.Accept(ctx, candidate, invited.ID, "0.9"); !errors.Is(err, enrollment.ErrTOSVersionMismatch) {
		t.Errorf("stale TOS error = %v, want ErrTOSVersionMismatch", err)
	}
	accepted, err := uc.Accept(ctx, candidate, invited.ID, tosVersion)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if accepted.TOSVersion == nil || *accepted.TOSVersion != tosVersion {
		t.Fatalf("accepted = %+v", accepted)
	}

	// Approve of a suspended account rolls the whole transition back.
	suspended := auth.AccountStatusSuspended
	if _, err := accounts.UpdateAccount(ctx, auth.UpdateAccount{ID: candidate, Status: new(suspended)}); err != nil {
		t.Fatalf("suspending candidate: %v", err)
	}
	if _, err := uc.Approve(ctx, admin, invited.ID); !errors.Is(err, enrollment.ErrNotEligible) {
		t.Fatalf("approve-suspended error = %v, want ErrNotEligible", err)
	}
	still, err := uc.MyEnrollment(ctx, candidate)
	if err != nil || still.Status != enrollment.StatusAccepted {
		t.Fatalf("enrollment after rolled-back approve = %+v, %v — the approval must not stick", still, err)
	}
	if got := role(ctx, t, accounts, candidate); got != auth.AccountRolePendingAgent {
		t.Fatalf("role after rolled-back approve = %q", got)
	}

	// Reactivate and approve for real: role → agent, second notification.
	active := auth.AccountStatusActive
	if _, err := accounts.UpdateAccount(ctx, auth.UpdateAccount{ID: candidate, Status: new(active)}); err != nil {
		t.Fatalf("reactivating candidate: %v", err)
	}
	approved, err := uc.Approve(ctx, admin, invited.ID)
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if approved.Status != enrollment.StatusApproved {
		t.Fatalf("approved = %+v", approved)
	}
	if got := role(ctx, t, accounts, candidate); got != auth.AccountRoleAgent {
		t.Fatalf("role after approve = %q, want agent", got)
	}
	if len(notifier.events) != 2 || notifier.events[1] != "enrollment.approved" {
		t.Errorf("events after approve = %v", notifier.events)
	}
}

func TestEnrollmentUseCaseDeclineAndCancel(t *testing.T) {
	uc, _, accounts := newTestUseCase(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	admin := testAccount(ctx, t, accounts, "Admin")

	// Decline restores the user role.
	decliner := testAccount(ctx, t, accounts, "Decliner")
	inv, err := uc.Invite(ctx, decliner, admin)
	if err != nil {
		t.Fatalf("Invite: %v", err)
	}
	if _, err := uc.Decline(ctx, decliner, inv.ID); err != nil {
		t.Fatalf("Decline: %v", err)
	}
	if got := role(ctx, t, accounts, decliner); got != auth.AccountRoleUser {
		t.Fatalf("role after decline = %q, want user", got)
	}

	// Cancel from accepted restores the user role too.
	inv2, err := uc.Invite(ctx, decliner, admin)
	if err != nil {
		t.Fatalf("re-Invite: %v", err)
	}
	if _, err := uc.Accept(ctx, decliner, inv2.ID, tosVersion); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if _, err := uc.Cancel(ctx, admin, inv2.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if got := role(ctx, t, accounts, decliner); got != auth.AccountRoleUser {
		t.Fatalf("role after cancel = %q, want user", got)
	}

	// An ops account can never be invited.
	opsRole := auth.AccountRoleOps
	ops := testAccount(ctx, t, accounts, "Ops")
	if _, err := accounts.UpdateAccount(ctx, auth.UpdateAccount{ID: ops, Role: new(opsRole)}); err != nil {
		t.Fatalf("promoting ops: %v", err)
	}
	if _, err := uc.Invite(ctx, ops, admin); !errors.Is(err, enrollment.ErrNotEligible) {
		t.Errorf("invite-ops error = %v, want ErrNotEligible", err)
	}
}
