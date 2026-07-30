package repo_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"mokapay.app/app/services/payment/internal/business/agentprofile"
	profilerepo "mokapay.app/app/services/payment/internal/business/agentprofile/repo"
	"mokapay.app/app/services/payment/internal/business/commission"
	"mokapay.app/app/services/payment/internal/business/commission/repo"
	"mokapay.app/app/services/payment/internal/business/ledger"
	ledgerrepo "mokapay.app/app/services/payment/internal/business/ledger/repo"
	"mokapay.app/app/services/payment/internal/business/transfer"
	transferrepo "mokapay.app/app/services/payment/internal/business/transfer/repo"
	coredb "mokapay.app/core/db"
)

// Integration tests: need a migrated payments database. Skipped unless
// TEST_DB_PORT is set:
//
//	TEST_DB_PORT=5433 go test ./internal/business/...
type stores struct {
	commissions repo.Store
	transfers   transferrepo.Store
	profiles    profilerepo.Store
	requests    ledgerrepo.Store
}

func newStores(t *testing.T) stores {
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

	return stores{
		commissions: repo.New(db),
		transfers:   transferrepo.New(db),
		profiles:    profilerepo.New(db),
		requests:    ledgerrepo.New(db),
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// agent builds a pending profile — commissions never look at the status,
// only at the id, so there is nothing to activate here.
func agent(ctx context.Context, t *testing.T, s stores) agentprofile.AgentProfile {
	t.Helper()

	p, err := s.profiles.Add(ctx, agentprofile.NewAgentProfile{AccountID: uuid.New()})
	if err != nil {
		t.Fatalf("creating profile: %v", err)
	}
	return p
}

// usdTransfer creates a usd→usd transfer (fee 1.5%, no margin) — the
// accrual only needs a real transfer id to point at.
func usdTransfer(ctx context.Context, t *testing.T, s stores, from int64) transfer.Transfer {
	t.Helper()

	fee := (from*150 + 5_000) / 10_000
	tr, err := s.transfers.Add(ctx, uuid.New(), transfer.NewTransfer{
		RecipientName:     "Recipient",
		RecipientPhone:    "+243811111111",
		RecipientOperator: transfer.OperatorOrangeMoney,
		FromCurrency:      transfer.CurrencyUSD,
		FromOperator:      transfer.OperatorOrangeMoney,
		FromAmount:        from,
		ToCurrency:        transfer.CurrencyUSD,
		ToAmount:          from,
		FeePctBps:         150,
		FeeAmount:         fee,
		HoldAmount:        from + fee,
	})
	if err != nil {
		t.Fatalf("creating transfer: %v", err)
	}
	return tr
}

// accrue books one commission for the agent in the given week.
func accrue(ctx context.Context, t *testing.T, s stores, profileID uuid.UUID, week time.Time, from int64) commission.Commission {
	t.Helper()

	tr := usdTransfer(ctx, t, s, from)
	tr.AgentProfileID = new(profileID)

	nc, err := commission.NewAccrual(tr, week)
	if err != nil {
		t.Fatalf("deriving accrual: %v", err)
	}
	c, err := s.commissions.Accrue(ctx, nc)
	if err != nil {
		t.Fatalf("accruing: %v", err)
	}
	return c
}

// TestAccrualIsIdempotent is THE guarantee of this domain: one accrual
// per transfer, whatever replays it. It is what makes a commission
// backfill script unnecessary — and it must stay that way.
func TestAccrualIsIdempotent(t *testing.T) {
	s := newStores(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	p := agent(ctx, t, s)
	tr := usdTransfer(ctx, t, s, 10_000)
	tr.AgentProfileID = new(p.ID)

	nc, err := commission.NewAccrual(tr, time.Now())
	if err != nil {
		t.Fatalf("deriving accrual: %v", err)
	}

	first, err := s.commissions.Accrue(ctx, nc)
	if err != nil {
		t.Fatalf("first accrual: %v", err)
	}
	// 150 cents of fee: 90 / 45 / 15, all of it USD, nothing payable lost.
	if first.AgentAmount != 90 || first.PlatformAmount != 45 || first.GuaranteeFundAmount != 15 {
		t.Fatalf("stored split = %d/%d/%d, want 90/45/15",
			first.AgentAmount, first.PlatformAmount, first.GuaranteeFundAmount)
	}
	if first.PayableAmount != 90 {
		t.Fatalf("payable = %d, want 90", first.PayableAmount)
	}

	// Every path to `done` accrues, so a replay (redelivered completion,
	// dispute resolved on an already-completed transfer) must bounce.
	if _, err := s.commissions.Accrue(ctx, nc); !errors.Is(err, commission.ErrCommissionExists) {
		t.Fatalf("second accrual = %v, want ErrCommissionExists", err)
	}

	// And exactly one row exists for the transfer.
	got, err := s.commissions.GetByTransfer(ctx, tr.ID.String())
	if err != nil {
		t.Fatalf("GetByTransfer: %v", err)
	}
	if got.ID != first.ID {
		t.Fatalf("GetByTransfer id = %s, want %s", got.ID, first.ID)
	}
	if got.Status != commission.StatusAccrued {
		t.Fatalf("status = %s, want accrued", got.Status)
	}
	if got.BatchID != nil {
		t.Fatalf("a fresh accrual must not be batched: %v", got.BatchID)
	}
}

// TestWeeklyBatchLifecycle walks a whole payout: accrue → due → batch →
// settle, and proves the derived status follows the ledger request rather
// than a stored copy.
func TestWeeklyBatchLifecycle(t *testing.T) {
	s := newStores(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	p := agent(ctx, t, s)
	// A closed week in the past: the run only ever pays closed weeks.
	week := commission.AccountingWeek(time.Now().AddDate(0, 0, -14))

	first := accrue(ctx, t, s, p.ID, week, 10_000)  // fee 150 -> agent 90
	second := accrue(ctx, t, s, p.ID, week, 20_000) // fee 300 -> agent 180
	wantTotal := first.PayableAmount + second.PayableAmount
	if wantTotal != 270 {
		t.Fatalf("fixture total = %d, want 270", wantTotal)
	}

	// The worklist sees exactly what is unpaid for this agent.
	due, err := s.commissions.ListDue(ctx, commission.AccountingWeek(time.Now()))
	if err != nil {
		t.Fatalf("ListDue: %v", err)
	}
	mine := dueOf(due, p.ID)
	if mine == nil {
		t.Fatal("ListDue: the agent's closed week is missing")
	}
	if mine.CommissionCount != 2 || mine.TotalAmount != wantTotal {
		t.Fatalf("due = %d rows / %d, want 2 / %d", mine.CommissionCount, mine.TotalAmount, wantTotal)
	}

	// A wrong total (a concurrent accrual) must never be paid out.
	req := commissionRequest(ctx, t, s, p.ID, wantTotal)
	if _, err := s.commissions.AddBatch(ctx, p.ID, week, wantTotal+1, req.ID); !errors.Is(err, commission.ErrBatchNotCreated) {
		t.Fatalf("AddBatch with a stale total = %v, want ErrBatchNotCreated", err)
	}

	batch, err := s.commissions.AddBatch(ctx, p.ID, week, wantTotal, req.ID)
	if err != nil {
		t.Fatalf("AddBatch: %v", err)
	}
	if batch.CommissionCount != 2 || batch.TotalAmount != wantTotal {
		t.Fatalf("batch = %d rows / %d, want 2 / %d", batch.CommissionCount, batch.TotalAmount, wantTotal)
	}

	// The accruals now belong to the batch and read as `batched` — the
	// ledger request has not settled yet.
	for _, c := range listMine(ctx, t, s, p.ID) {
		if c.BatchID == nil || *c.BatchID != batch.ID {
			t.Fatalf("commission %s not linked to the batch: %v", c.ID, c.BatchID)
		}
		if c.Status != commission.StatusBatched {
			t.Fatalf("commission %s status = %s, want batched", c.ID, c.Status)
		}
	}

	// One payout per agent per week: the re-run finds nothing to do.
	if _, err := s.commissions.AddBatch(ctx, p.ID, week, wantTotal, req.ID); !errors.Is(err, commission.ErrBatchNotCreated) {
		t.Fatalf("re-running the batch = %v, want ErrBatchNotCreated", err)
	}
	if due, err := s.commissions.ListDue(ctx, commission.AccountingWeek(time.Now())); err != nil {
		t.Fatalf("ListDue after batching: %v", err)
	} else if dueOf(due, p.ID) != nil {
		t.Fatal("ListDue still reports a batched week")
	}

	// Settling the request is what means "paid" — the status is derived
	// from it, never stored, so the two can never disagree.
	if _, err := s.requests.Approve(ctx, req.ID.String(), uuid.NewString()); err != nil {
		t.Fatalf("approving the commission request: %v", err)
	}

	batches, err := s.commissions.ListBatches(ctx, commission.BatchFilter{AgentProfileID: new(p.ID)}, 10)
	if err != nil {
		t.Fatalf("ListBatches: %v", err)
	}
	if len(batches) != 1 || batches[0].Status != commission.StatusPaid {
		t.Fatalf("batches = %+v, want one paid", batches)
	}
	for _, c := range listMine(ctx, t, s, p.ID) {
		if c.Status != commission.StatusPaid {
			t.Fatalf("commission %s status = %s, want paid", c.ID, c.Status)
		}
	}

	// And the credit actually landed on the balance.
	profile, err := s.profiles.Get(ctx, p.ID.String())
	if err != nil {
		t.Fatalf("reading profile: %v", err)
	}
	if profile.Balance != wantTotal {
		t.Fatalf("balance = %d, want %d", profile.Balance, wantTotal)
	}
}

// commissionRequest opens the inflow the batch settles through.
func commissionRequest(ctx context.Context, t *testing.T, s stores, profileID uuid.UUID, amount int64) ledger.LedgerRequest {
	t.Helper()

	req, err := s.requests.Add(ctx, ledger.StatusPendingAdminApproval, ledger.DirectionInflow, uuid.Nil, ledger.NewRequest{
		AgentProfileID: profileID,
		Type:           ledger.TypeCommission,
		Amount:         amount,
	})
	if err != nil {
		t.Fatalf("creating commission request: %v", err)
	}
	return req
}

func listMine(ctx context.Context, t *testing.T, s stores, profileID uuid.UUID) []commission.Commission {
	t.Helper()

	list, err := s.commissions.List(ctx, commission.Filter{AgentProfileID: new(profileID)}, 50)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	return list
}

func dueOf(due []commission.Due, profileID uuid.UUID) *commission.Due {
	for i, d := range due {
		if d.AgentProfileID == profileID {
			return &due[i]
		}
	}
	return nil
}
