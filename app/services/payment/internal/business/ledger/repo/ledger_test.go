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

	req, err := requests.Add(ctx, ledger.StatusInitiated, ledger.DirectionInflow, uuid.New(), ledger.NewRequest{
		AgentProfileID: profileID,
		Type:           ledger.TypeFunding,
		Amount:         amount,
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
	ref := ledger.Reference{Type: ledger.ReferenceEnrollment, ID: uuid.New()}
	req, err := requests.Add(ctx, ledger.StatusInitiated, ledger.DirectionInflow, uuid.New(), ledger.NewRequest{
		AgentProfileID: p.ID,
		Type:           ledger.TypeFunding,
		Amount:         2000,
		Reference:      new(ref),
	})
	if err != nil {
		t.Fatalf("Add funding request: %v", err)
	}
	if req.Status != ledger.StatusInitiated {
		t.Fatalf("new request status = %q", req.Status)
	}
	if req.Reference == nil || req.Reference.ID != ref.ID || req.Reference.Type != ledger.ReferenceEnrollment {
		t.Fatalf("reference round-trip = %+v", req.Reference)
	}

	// Replay: the entity already has a live request → ErrReferenceInUse,
	// and GetByReference finds the original.
	if _, err := requests.Add(ctx, ledger.StatusInitiated, ledger.DirectionInflow, uuid.New(), ledger.NewRequest{
		AgentProfileID: p.ID,
		Type:           ledger.TypeFunding,
		Amount:         2000,
		Reference:      new(ref),
	}); !errors.Is(err, ledger.ErrReferenceInUse) {
		t.Errorf("replayed create error = %v, want ErrReferenceInUse", err)
	}
	byRef, err := requests.GetByReference(ctx, ref)
	if err != nil || byRef.ID != req.ID {
		t.Fatalf("GetByReference = %+v, %v", byRef, err)
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
	// The admin can send it back for a better proof, and the agent can
	// re-forward it — the send-back loop of the state machine.
	if _, err := requests.UpdateStatus(ctx, req.ID.String(), ledger.StatusPendingAgentAction, nil); err != nil {
		t.Fatalf("send-back move: %v", err)
	}
	if _, err := requests.UpdateStatus(ctx, req.ID.String(), ledger.StatusPendingAdminApproval, nil); err != nil {
		t.Fatalf("re-forward move: %v", err)
	}
	// Closing states remain unreachable through UpdateStatus.
	if _, err := requests.UpdateStatus(ctx, req.ID.String(), ledger.StatusDone, nil); !errors.Is(err, ledger.ErrRequestNotFound) {
		t.Errorf("UpdateStatus to done error = %v, want ErrRequestNotFound", err)
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
	if _, err := requests.Add(ctx, ledger.StatusInitiated, ledger.DirectionOutflow, uuid.New(), ledger.NewRequest{
		AgentProfileID: p.ID,
		Type:           ledger.TypeWithdrawal,
		Amount:         10_000,
	}); !errors.Is(err, ledger.ErrNotFeasible) {
		t.Fatalf("oversized withdrawal error = %v, want ErrNotFeasible", err)
	}

	// The guarantee floor is not withdrawable: 3001 fails, 3000 passes.
	if _, err := requests.Add(ctx, ledger.StatusInitiated, ledger.DirectionOutflow, uuid.New(), ledger.NewRequest{
		AgentProfileID: p.ID,
		Type:           ledger.TypeWithdrawal,
		Amount:         3001,
	}); !errors.Is(err, ledger.ErrNotFeasible) {
		t.Fatalf("floor-busting withdrawal error = %v, want ErrNotFeasible", err)
	}
	wd, err := requests.Add(ctx, ledger.StatusInitiated, ledger.DirectionOutflow, uuid.New(), ledger.NewRequest{
		AgentProfileID: p.ID,
		Type:           ledger.TypeWithdrawal,
		Amount:         3000,
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
	wd2, err := requests.Add(ctx, ledger.StatusInitiated, ledger.DirectionOutflow, uuid.New(), ledger.NewRequest{
		AgentProfileID: p.ID,
		Type:           ledger.TypeWithdrawal,
		Amount:         3000,
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
	if _, err := requests.Add(ctx, ledger.StatusInitiated, ledger.DirectionOutflow, uuid.New(), ledger.NewRequest{
		AgentProfileID: pending.ID,
		Type:           ledger.TypeWithdrawal,
		Amount:         1,
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
	req, err := requests.Add(ctx, ledger.StatusInitiated, ledger.DirectionInflow, uuid.New(), ledger.NewRequest{
		AgentProfileID: p.ID,
		Type:           ledger.TypeFunding,
		Amount:         1000,
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
			if _, err := requests.Add(ctx, ledger.StatusInitiated, ledger.DirectionOutflow, uuid.New(), ledger.NewRequest{
				AgentProfileID: p.ID,
				Type:           ledger.TypeWithdrawal,
				Amount:         300,
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

// TestReferenceSlotFreedByRejection proves the partial unique index: a
// rejected request frees the entity's slot (a rejected guarantee deposit
// must be retryable), while done keeps blocking.
func TestReferenceSlotFreedByRejection(t *testing.T) {
	requests, profiles := newTestStores(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	p, err := profiles.Add(ctx, agentprofile.NewAgentProfile{AccountID: uuid.New()})
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	ref := ledger.Reference{Type: ledger.ReferenceEnrollment, ID: uuid.New()}
	nr := ledger.NewRequest{
		AgentProfileID: p.ID,
		Type:           ledger.TypeFunding,
		Amount:         2000,
		Reference:      new(ref),
	}

	first, err := requests.Add(ctx, ledger.StatusInitiated, ledger.DirectionInflow, uuid.New(), nr)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Rejecting frees the slot…
	if _, err := requests.Reject(ctx, first.ID.String(), uuid.NewString(), new("bad proof")); err != nil {
		t.Fatalf("Reject: %v", err)
	}
	second, err := requests.Add(ctx, ledger.StatusInitiated, ledger.DirectionInflow, uuid.New(), nr)
	if err != nil {
		t.Fatalf("Add after reject: %v", err)
	}
	// …and GetByReference now resolves to the live retry, not the corpse.
	byRef, err := requests.GetByReference(ctx, ref)
	if err != nil || byRef.ID != second.ID {
		t.Fatalf("GetByReference after reject = %+v, %v", byRef, err)
	}

	// Settling the retry blocks the slot for good.
	if _, err := requests.UpdateStatus(ctx, second.ID.String(), ledger.StatusPendingAdminApproval, nil); err != nil {
		t.Fatalf("forward: %v", err)
	}
	if _, err := requests.Approve(ctx, second.ID.String(), uuid.NewString()); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if _, err := requests.Add(ctx, ledger.StatusInitiated, ledger.DirectionInflow, uuid.New(), nr); !errors.Is(err, ledger.ErrReferenceInUse) {
		t.Errorf("Add after done error = %v, want ErrReferenceInUse", err)
	}
}

// TestCreateLandingStatus proves a request lands with its next actor and
// that closing statuses can never be creation inputs (bypassing
// settlement is structurally impossible).
func TestCreateLandingStatus(t *testing.T) {
	requests, profiles := newTestStores(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	p, err := profiles.Add(ctx, agentprofile.NewAgentProfile{AccountID: uuid.New()})
	if err != nil {
		t.Fatalf("profile: %v", err)
	}

	// Admin-created funding: the agent acts next.
	req, err := requests.Add(ctx, ledger.StatusPendingAgentAction, ledger.DirectionInflow, uuid.New(), ledger.NewRequest{
		AgentProfileID: p.ID,
		Type:           ledger.TypeFunding,
		Amount:         2000,
	})
	if err != nil {
		t.Fatalf("Add at pending_agent_action: %v", err)
	}
	if req.Status != ledger.StatusPendingAgentAction {
		t.Fatalf("landing status = %q, want pending_agent_action", req.Status)
	}
	// The proof path continues forward from there as usual.
	if _, err := requests.UpdateStatus(ctx, req.ID.String(), ledger.StatusPendingAdminApproval, nil); err != nil {
		t.Fatalf("forward from landing status: %v", err)
	}

	// Creating directly in a closing status is impossible.
	for _, closed := range []ledger.Status{ledger.StatusDone, ledger.StatusRejected} {
		if _, err := requests.Add(ctx, closed, ledger.DirectionInflow, uuid.New(), ledger.NewRequest{
			AgentProfileID: p.ID,
			Type:           ledger.TypeFunding,
			Amount:         100,
		}); !errors.Is(err, ledger.ErrNotFeasible) {
			t.Errorf("Add at %s error = %v, want ErrNotFeasible", closed, err)
		}
	}

	// The empty status still defaults to initiated.
	free, err := requests.Add(ctx, "", ledger.DirectionInflow, uuid.New(), ledger.NewRequest{
		AgentProfileID: p.ID,
		Type:           ledger.TypeFunding,
		Amount:         100,
	})
	if err != nil || free.Status != ledger.StatusInitiated {
		t.Fatalf("default status = %+v, %v", free.Status, err)
	}
}
