package repo_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"mokapay.app/app/services/payment/internal/business/agentprofile"
	profilerepo "mokapay.app/app/services/payment/internal/business/agentprofile/repo"
	"mokapay.app/app/services/payment/internal/business/ledger"
	"mokapay.app/app/services/payment/internal/business/ledger/repo"
	coredb "mokapay.app/core/db"
)

// Integration tests: need a migrated payments database. Skipped unless
// TEST_DB_PORT is set:
//
//	TEST_DB_PORT=5433 go test ./internal/business/...
func newTestStores(t *testing.T) (repo.Store, profilerepo.Store) {
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

	return repo.New(db), profilerepo.New(db)
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// activeProfile creates an active profile and funds it to the given
// balance through a full funding request cycle — the fixture exercises
// the whole machine.
func activeProfile(ctx context.Context, t *testing.T, requests repo.Store, profiles profilerepo.Store, balance int64, floor int64) agentprofile.AgentProfile {
	t.Helper()

	p, err := profiles.Add(ctx, agentprofile.NewAgentProfile{
		AccountID:      uuid.New(),
		GuaranteeFloor: new(floor),
	})
	if err != nil {
		t.Fatalf("creating profile: %v", err)
	}

	if balance > 0 {
		fund(ctx, t, requests, p.ID, balance)
	}

	p2, err := profiles.SetStatus(ctx, p.ID.String(), agentprofile.StatusActive)
	if err != nil {
		t.Fatalf("activating profile: %v", err)
	}
	return p2
}

// fund runs one full funding cycle: create (inflow, works on pending
// profiles) → forward to approval → approve.
func fund(ctx context.Context, t *testing.T, requests repo.Store, profileID uuid.UUID, amount int64) ledger.LedgerRequest {
	t.Helper()

	req, err := requests.Add(ctx, ledger.NewRequest{
		AgentProfileID: profileID,
		Type:           ledger.TypeFunding,
		Direction:      ledger.DirectionInflow,
		Amount:         amount,
		IdempotencyKey: uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("creating funding request: %v", err)
	}
	if _, err := requests.UpdateStatus(ctx, req.ID.String(), ledger.StatusPendingAdminApproval, nil); err != nil {
		t.Fatalf("forwarding funding request: %v", err)
	}
	done, err := requests.Approve(ctx, req.ID.String(), uuid.NewString())
	if err != nil {
		t.Fatalf("approving funding request: %v", err)
	}
	return done
}

func TestLedgerFundingLifecycle(t *testing.T) {
	requests, profiles := newTestStores(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	p, err := profiles.Add(ctx, agentprofile.NewAgentProfile{AccountID: uuid.New()})
	if err != nil {
		t.Fatalf("Add profile: %v", err)
	}
	if p.Status != agentprofile.StatusPending || p.GuaranteeFloor != 2000 || p.Currency != "USD" {
		t.Fatalf("profile defaults = %+v", p)
	}

	// Duplicate profile for the same account is rejected.
	if _, err := profiles.Add(ctx, agentprofile.NewAgentProfile{AccountID: p.AccountID}); !errors.Is(err, agentprofile.ErrProfileExists) {
		t.Errorf("duplicate profile error = %v, want ErrProfileExists", err)
	}

	// Funding (inflow) works on a PENDING profile — the enrollment
	// guarantee funds a not-yet-active agent.
	enrollment := uuid.New()
	req, err := requests.Add(ctx, ledger.NewRequest{
		AgentProfileID: p.ID,
		Type:           ledger.TypeFunding,
		Direction:      ledger.DirectionInflow,
		Amount:         2000,
		IdempotencyKey: enrollment.String(),
		EnrollmentID:   new(enrollment),
	})
	if err != nil {
		t.Fatalf("Add funding request: %v", err)
	}
	if req.Status != ledger.StatusInitiated {
		t.Fatalf("new request status = %q", req.Status)
	}

	// Idempotency: same key → ErrDuplicateKey, and GetByKey finds the
	// original (no reservation side effects for inflows anyway).
	if _, err := requests.Add(ctx, ledger.NewRequest{
		AgentProfileID: p.ID,
		Type:           ledger.TypeFunding,
		Direction:      ledger.DirectionInflow,
		Amount:         2000,
		IdempotencyKey: enrollment.String(),
	}); !errors.Is(err, ledger.ErrDuplicateKey) {
		t.Errorf("duplicate key error = %v, want ErrDuplicateKey", err)
	}
	byKey, err := requests.GetByKey(ctx, enrollment.String())
	if err != nil || byKey.ID != req.ID {
		t.Fatalf("GetByKey = %+v, %v", byKey, err)
	}

	// Proof attach + forward; a repeated (redelivered) update is a no-op
	// error the caller tolerates.
	proof := uuid.New()
	forwarded, err := requests.UpdateStatus(ctx, req.ID.String(), ledger.StatusPendingAdminApproval, new(proof))
	if err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	if forwarded.ProofFileID == nil || *forwarded.ProofFileID != proof {
		t.Fatalf("proof not attached: %+v", forwarded)
	}
	if _, err := requests.UpdateStatus(ctx, req.ID.String(), ledger.StatusPendingAgentAction, nil); !errors.Is(err, ledger.ErrRequestNotFound) {
		t.Errorf("backward move error = %v, want ErrRequestNotFound", err)
	}

	// Approve settles: balance moves, one line with balance_after.
	admin := uuid.New()
	done, err := requests.Approve(ctx, req.ID.String(), admin.String())
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if done.Status != ledger.StatusDone || done.DecidedBy == nil || *done.DecidedBy != admin {
		t.Fatalf("approved = %+v", done)
	}

	after, err := profiles.Get(ctx, p.ID.String())
	if err != nil || after.Balance != 2000 || after.Held != 0 || after.Available != 2000 {
		t.Fatalf("profile after settle = %+v, %v", after, err)
	}

	lines, err := requests.ListLines(ctx, p.ID.String(), 10)
	if err != nil || len(lines) != 1 {
		t.Fatalf("lines = %v, %v", lines, err)
	}
	if lines[0].Amount != 2000 || lines[0].BalanceAfter != 2000 || lines[0].RequestID != req.ID {
		t.Fatalf("line = %+v", lines[0])
	}

	// A second approve finds nothing to settle…
	if _, err := requests.Approve(ctx, req.ID.String(), admin.String()); !errors.Is(err, ledger.ErrRequestNotFound) {
		t.Errorf("re-approve error = %v, want ErrRequestNotFound", err)
	}
	// …and the money moved exactly once.
	if after, _ := profiles.Get(ctx, p.ID.String()); after.Balance != 2000 {
		t.Fatalf("balance after re-approve = %d", after.Balance)
	}

	// Deleting a funded profile is impossible.
	if err := profiles.Delete(ctx, p.ID.String()); !errors.Is(err, agentprofile.ErrProfileHasFunds) {
		t.Errorf("delete funded profile error = %v, want ErrProfileHasFunds", err)
	}
}

func TestLedgerReservationSemantics(t *testing.T) {
	requests, profiles := newTestStores(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Active profile: balance 5000, floor 2000 → available 5000,
	// withdrawable 3000.
	p := activeProfile(ctx, t, requests, profiles, 5000, 2000)

	// "Withdraw 100 with 20 on the books": infeasible on the spot.
	if _, err := requests.Add(ctx, ledger.NewRequest{
		AgentProfileID: p.ID,
		Type:           ledger.TypeWithdrawal,
		Direction:      ledger.DirectionOutflow,
		Amount:         10_000,
		IdempotencyKey: uuid.NewString(),
	}); !errors.Is(err, ledger.ErrNotFeasible) {
		t.Fatalf("oversized withdrawal error = %v, want ErrNotFeasible", err)
	}

	// The guarantee floor is not withdrawable: 3001 fails, 3000 passes.
	if _, err := requests.Add(ctx, ledger.NewRequest{
		AgentProfileID: p.ID,
		Type:           ledger.TypeWithdrawal,
		Direction:      ledger.DirectionOutflow,
		Amount:         3001,
		IdempotencyKey: uuid.NewString(),
	}); !errors.Is(err, ledger.ErrNotFeasible) {
		t.Fatalf("floor-busting withdrawal error = %v, want ErrNotFeasible", err)
	}
	wd, err := requests.Add(ctx, ledger.NewRequest{
		AgentProfileID: p.ID,
		Type:           ledger.TypeWithdrawal,
		Direction:      ledger.DirectionOutflow,
		Amount:         3000,
		IdempotencyKey: uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("withdrawal create: %v", err)
	}

	// The reservation is live: held = 3000, available = 5000-3000.
	mid, _ := profiles.Get(ctx, p.ID.String())
	if mid.Held != 3000 || mid.Available != 2000 {
		t.Fatalf("profile mid-withdrawal = balance %d held %d available %d", mid.Balance, mid.Held, mid.Available)
	}

	// Reject releases it.
	if _, err := requests.Reject(ctx, wd.ID.String(), uuid.NewString(), new("changed my mind")); err != nil {
		t.Fatalf("Reject: %v", err)
	}
	released, _ := profiles.Get(ctx, p.ID.String())
	if released.Held != 0 || released.Balance != 5000 {
		t.Fatalf("profile after reject = %+v", released)
	}

	// Same withdrawal again, approved this time: the crossover.
	wd2, err := requests.Add(ctx, ledger.NewRequest{
		AgentProfileID: p.ID,
		Type:           ledger.TypeWithdrawal,
		Direction:      ledger.DirectionOutflow,
		Amount:         3000,
		IdempotencyKey: uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("withdrawal re-create: %v", err)
	}
	if _, err := requests.UpdateStatus(ctx, wd2.ID.String(), ledger.StatusPendingAdminApproval, nil); err != nil {
		t.Fatalf("forward: %v", err)
	}
	if _, err := requests.Approve(ctx, wd2.ID.String(), uuid.NewString()); err != nil {
		t.Fatalf("Approve withdrawal: %v", err)
	}
	final, _ := profiles.Get(ctx, p.ID.String())
	if final.Balance != 2000 || final.Held != 0 || final.Available != 2000 {
		t.Fatalf("profile after crossover = balance %d held %d", final.Balance, final.Held)
	}

	lines, _ := requests.ListLines(ctx, p.ID.String(), 10)
	if len(lines) != 2 || lines[0].Amount != -3000 || lines[0].BalanceAfter != 2000 {
		t.Fatalf("lines after withdrawal = %+v", lines)
	}

	// Outflows never work on a pending profile.
	pending, err := profiles.Add(ctx, agentprofile.NewAgentProfile{AccountID: uuid.New()})
	if err != nil {
		t.Fatalf("pending profile: %v", err)
	}
	if _, err := requests.Add(ctx, ledger.NewRequest{
		AgentProfileID: pending.ID,
		Type:           ledger.TypeWithdrawal,
		Direction:      ledger.DirectionOutflow,
		Amount:         1,
		IdempotencyKey: uuid.NewString(),
	}); !errors.Is(err, ledger.ErrNotFeasible) {
		t.Errorf("outflow on pending error = %v, want ErrNotFeasible", err)
	}
}

// TestConcurrentApproves proves settlement happens exactly once under a
// stampede of racing approvals.
func TestConcurrentApproves(t *testing.T) {
	requests, profiles := newTestStores(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	p := activeProfile(ctx, t, requests, profiles, 0, 0)
	req, err := requests.Add(ctx, ledger.NewRequest{
		AgentProfileID: p.ID,
		Type:           ledger.TypeFunding,
		Direction:      ledger.DirectionInflow,
		Amount:         1000,
		IdempotencyKey: uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := requests.UpdateStatus(ctx, req.ID.String(), ledger.StatusPendingAdminApproval, nil); err != nil {
		t.Fatalf("forward: %v", err)
	}

	const racers = 8
	var wg sync.WaitGroup
	wins := make(chan struct{}, racers)
	for range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := requests.Approve(ctx, req.ID.String(), uuid.NewString()); err == nil {
				wins <- struct{}{}
			}
		}()
	}
	wg.Wait()
	close(wins)

	if got := len(wins); got != 1 {
		t.Fatalf("%d approvals won, want exactly 1", got)
	}
	after, _ := profiles.Get(ctx, p.ID.String())
	if after.Balance != 1000 {
		t.Fatalf("balance after race = %d, want 1000 (moved exactly once)", after.Balance)
	}
	lines, _ := requests.ListLines(ctx, p.ID.String(), 10)
	if len(lines) != 1 {
		t.Fatalf("%d lines after race, want 1", len(lines))
	}
}

// TestConcurrentCreates proves racing outflow creates can never
// over-promise the same funds: with 1000 available, exactly three 300
// reservations fit.
func TestConcurrentCreates(t *testing.T) {
	requests, profiles := newTestStores(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	p := activeProfile(ctx, t, requests, profiles, 1000, 0)

	const racers = 8
	var wg sync.WaitGroup
	wins := make(chan struct{}, racers)
	for range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := requests.Add(ctx, ledger.NewRequest{
				AgentProfileID: p.ID,
				Type:           ledger.TypeWithdrawal,
				Direction:      ledger.DirectionOutflow,
				Amount:         300,
				IdempotencyKey: uuid.NewString(),
			}); err == nil {
				wins <- struct{}{}
			}
		}()
	}
	wg.Wait()
	close(wins)

	if got := len(wins); got != 3 {
		t.Fatalf("%d creates won, want exactly 3 (3×300 ≤ 1000 < 4×300)", got)
	}
	after, _ := profiles.Get(ctx, p.ID.String())
	if after.Held != 900 || after.Available != 100 {
		t.Fatalf("after race: held %d available %d, want 900/100", after.Held, after.Available)
	}
}
