package repo_test

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
	"mokapay.app/app/services/user/internal/business/enrollment/repo"
	coredb "mokapay.app/core/db"
)

// Integration tests: need a migrated users database (0003). Skipped unless
// TEST_DB_PORT is set:
//
//	TEST_DB_PORT=5433 go test ./internal/business/enrollment/repo/
func newTestStores(t *testing.T) (repo.Store, authrepo.Store) {
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

	return repo.New(db), authrepo.New(db)
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
		LastName:    "Enrollment",
	}, "")
	if err != nil {
		t.Fatalf("creating test account: %v", err)
	}
	return acc.ID
}

func TestEnrollmentHappyPath(t *testing.T) {
	store, accounts := newTestStores(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	candidate := testAccount(ctx, t, accounts, "Candidate")
	admin := testAccount(ctx, t, accounts, "Admin")

	// Invite.
	invited, err := store.Add(ctx, candidate.String(), admin.String())
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if invited.Status != enrollment.StatusInvited ||
		invited.InvitedBy == nil || *invited.InvitedBy != admin || invited.SelfServe() {
		t.Fatalf("invite = %+v", invited)
	}

	// A second live invite is rejected.
	if _, err := store.Add(ctx, candidate.String(), admin.String()); !errors.Is(err, enrollment.ErrEnrollmentPending) {
		t.Errorf("double invite error = %v, want ErrEnrollmentPending", err)
	}

	// GetLatest sees it.
	latest, err := store.GetLatestByAccount(ctx, candidate.String())
	if err != nil || latest.ID != invited.ID {
		t.Fatalf("GetLatestByAccount = %+v, %v", latest, err)
	}

	// A foreign account can't accept it.
	stranger := testAccount(ctx, t, accounts, "Stranger")
	if _, err := store.Accept(ctx, invited.ID.String(), stranger.String(), "v1"); !errors.Is(err, enrollment.ErrEnrollmentNotFound) {
		t.Errorf("foreign accept error = %v, want ErrEnrollmentNotFound", err)
	}

	// Accept stamps the terms.
	accepted, err := store.Accept(ctx, invited.ID.String(), candidate.String(), "2026-07-25")
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if accepted.Status != enrollment.StatusAccepted ||
		accepted.TOSVersion == nil || *accepted.TOSVersion != "2026-07-25" ||
		accepted.TOSAcceptedAt == nil {
		t.Fatalf("accepted = %+v", accepted)
	}

	// Decline after accept is impossible (guard: only from invited).
	if _, err := store.Decline(ctx, invited.ID.String(), candidate.String()); !errors.Is(err, enrollment.ErrEnrollmentNotFound) {
		t.Errorf("decline-after-accept error = %v, want ErrEnrollmentNotFound", err)
	}

	// Approve stamps the decision.
	approved, err := store.Approve(ctx, invited.ID.String(), admin.String())
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if approved.Status != enrollment.StatusApproved ||
		approved.DecidedBy == nil || *approved.DecidedBy != admin ||
		approved.DecidedAt == nil {
		t.Fatalf("approved = %+v", approved)
	}

	// Terminal: no further transitions.
	if _, err := store.Cancel(ctx, invited.ID.String(), admin.String()); !errors.Is(err, enrollment.ErrEnrollmentNotFound) {
		t.Errorf("cancel-after-approve error = %v, want ErrEnrollmentNotFound", err)
	}
	// The approved row no longer blocks a fresh invite.
	if _, err := store.Add(ctx, candidate.String(), admin.String()); err != nil {
		t.Errorf("re-invite after approved: %v", err)
	}
}

func TestEnrollmentDeclineAndCancelPaths(t *testing.T) {
	store, accounts := newTestStores(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	admin := testAccount(ctx, t, accounts, "Admin")

	// Decline path: invited → declined, candidate is their own decider,
	// and a re-invite works.
	candidate := testAccount(ctx, t, accounts, "Decliner")
	invited, err := store.Add(ctx, candidate.String(), admin.String())
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	declined, err := store.Decline(ctx, invited.ID.String(), candidate.String())
	if err != nil {
		t.Fatalf("Decline: %v", err)
	}
	if declined.Status != enrollment.StatusDeclined ||
		declined.DecidedBy == nil || *declined.DecidedBy != candidate {
		t.Fatalf("declined = %+v", declined)
	}
	reinvited, err := store.Add(ctx, candidate.String(), admin.String())
	if err != nil {
		t.Fatalf("re-invite after decline: %v", err)
	}

	// Approve straight from invited is impossible (TOS gate first).
	if _, err := store.Approve(ctx, reinvited.ID.String(), admin.String()); !errors.Is(err, enrollment.ErrEnrollmentNotFound) {
		t.Errorf("approve-from-invited error = %v, want ErrEnrollmentNotFound", err)
	}

	// Cancel path: works from invited…
	cancelled, err := store.Cancel(ctx, reinvited.ID.String(), admin.String())
	if err != nil {
		t.Fatalf("Cancel (invited): %v", err)
	}
	if cancelled.Status != enrollment.StatusCancelled {
		t.Fatalf("cancelled = %+v", cancelled)
	}

	// …and from accepted.
	third, err := store.Add(ctx, candidate.String(), admin.String())
	if err != nil {
		t.Fatalf("third invite: %v", err)
	}
	if _, err := store.Accept(ctx, third.ID.String(), candidate.String(), "v1"); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if _, err := store.Cancel(ctx, third.ID.String(), admin.String()); err != nil {
		t.Fatalf("Cancel (accepted): %v", err)
	}

	// List with status filter finds the cancelled rows.
	cancelledStatus := enrollment.StatusCancelled
	list, err := store.List(ctx, enrollment.Filter{Status: new(cancelledStatus)})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	found := 0
	for _, e := range list {
		if e.AccountID == candidate {
			found++
		}
	}
	if found != 2 {
		t.Errorf("cancelled rows for candidate = %d, want 2", found)
	}
}

// The self-serve entry point shares the table, the state machine and the
// uniqueness index with the invited one. What it must NOT share is an
// inviter — that absence is the only discriminator the rest of the system
// has, so it is worth asserting rather than assuming.
func TestSelfServeEnrollment(t *testing.T) {
	store, accounts := newTestStores(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	candidate := testAccount(ctx, t, accounts, "SelfServe")
	admin := testAccount(ctx, t, accounts, "Admin")

	// Born accepted, terms stamped inline, no inviter.
	self, err := store.AddSelf(ctx, candidate.String(), "2026-07-25")
	if err != nil {
		t.Fatalf("AddSelf: %v", err)
	}
	if !self.SelfServe() || self.InvitedBy != nil {
		t.Fatalf("self-serve row carries an inviter: %+v", self)
	}
	if self.Status != enrollment.StatusAccepted ||
		self.TOSVersion == nil || *self.TOSVersion != "2026-07-25" ||
		self.TOSAcceptedAt == nil {
		t.Fatalf("self-serve row = %+v, want accepted with the terms stamped", self)
	}

	// The one live-enrollment index covers both entry points: neither a
	// second self-serve request nor an invite gets through.
	if _, err := store.AddSelf(ctx, candidate.String(), "2026-07-25"); !errors.Is(err, enrollment.ErrEnrollmentPending) {
		t.Errorf("double self-enroll error = %v, want ErrEnrollmentPending", err)
	}
	if _, err := store.Add(ctx, candidate.String(), admin.String()); !errors.Is(err, enrollment.ErrEnrollmentPending) {
		t.Errorf("invite over a live self-serve error = %v, want ErrEnrollmentPending", err)
	}

	// Approve reaches it untouched — a self-serve row is already in the
	// state approve_enrollment requires, which is why that procedure
	// needed no change.
	approved, err := store.Approve(ctx, self.ID.String(), admin.String())
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if approved.Status != enrollment.StatusApproved || !approved.SelfServe() {
		t.Fatalf("approved = %+v", approved)
	}
	if approved.DecidedBy == nil || *approved.DecidedBy != admin {
		t.Fatalf("approved decided_by = %+v, want the settling admin", approved.DecidedBy)
	}
}
