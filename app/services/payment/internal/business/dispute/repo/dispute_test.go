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
	"mokapay.app/app/services/payment/internal/business/agentwallet"
	walletrepo "mokapay.app/app/services/payment/internal/business/agentwallet/repo"
	"mokapay.app/app/services/payment/internal/business/dispute"
	"mokapay.app/app/services/payment/internal/business/dispute/repo"
	"mokapay.app/app/services/payment/internal/business/ledger"
	ledgerrepo "mokapay.app/app/services/payment/internal/business/ledger/repo"
	"mokapay.app/app/services/payment/internal/business/matching"
	matchingrepo "mokapay.app/app/services/payment/internal/business/matching/repo"
	"mokapay.app/app/services/payment/internal/business/transfer"
	transferrepo "mokapay.app/app/services/payment/internal/business/transfer/repo"
	coredb "mokapay.app/core/db"
)

// Integration tests: need a migrated payments database. Skipped unless
// TEST_DB_PORT is set:
//
//	TEST_DB_PORT=5433 go test ./internal/business/...
type stores struct {
	disputes  repo.Store
	transfers transferrepo.Store
	offers    matchingrepo.Store
	profiles  profilerepo.Store
	wallets   walletrepo.Store
	requests  ledgerrepo.Store
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
		disputes:  repo.New(db),
		transfers: transferrepo.New(db),
		offers:    matchingrepo.New(db),
		profiles:  profilerepo.New(db),
		wallets:   walletrepo.New(db),
		requests:  ledgerrepo.New(db),
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// fundedAgent builds an ACTIVE profile with the given balance and a
// wallet on the test corridor.
func fundedAgent(ctx context.Context, t *testing.T, s stores, balance int64) agentprofile.AgentProfile {
	t.Helper()

	floor := int64(0)
	p, err := s.profiles.Add(ctx, agentprofile.NewAgentProfile{
		AccountID:      uuid.New(),
		GuaranteeFloor: new(floor),
	})
	if err != nil {
		t.Fatalf("creating profile: %v", err)
	}

	req, err := s.requests.Add(ctx, ledger.StatusPendingAdminApproval, ledger.DirectionInflow, uuid.New(), ledger.NewRequest{
		AgentProfileID: p.ID,
		Type:           ledger.TypeFunding,
		Amount:         balance,
	})
	if err != nil {
		t.Fatalf("creating funding request: %v", err)
	}
	if _, err := s.requests.Approve(ctx, req.ID.String(), uuid.NewString()); err != nil {
		t.Fatalf("approving funding request: %v", err)
	}

	active, err := s.profiles.SetStatus(ctx, p.ID.String(), agentprofile.StatusActive)
	if err != nil {
		t.Fatalf("activating profile: %v", err)
	}
	// The shared test DB persists across runs: suspend on the way out so
	// this agent never pollutes a future run's candidate shortlist.
	t.Cleanup(func() {
		_, _ = s.profiles.SetStatus(context.Background(), p.ID.String(), agentprofile.StatusSuspended)
	})

	if _, err := s.wallets.Add(ctx, active.ID, agentwallet.NewWallet{
		Operator: transfer.OperatorOrangeMoney, Msisdn: "+243990000002",
		AcceptsUSD: true, AcceptsCDF: true,
	}); err != nil {
		t.Fatalf("adding wallet: %v", err)
	}

	return active
}

// fundedTransfer walks a transfer to pending_agent_transfer: created,
// accepted by the agent (hold placed) and paid by the client. That is the
// high-risk state — the client is out their money and the agent has not
// paid out yet.
func fundedTransfer(ctx context.Context, t *testing.T, s stores, a agentprofile.AgentProfile, hold int64) transfer.Transfer {
	t.Helper()

	from := hold * 100 / 102
	fee := hold - from
	tr, err := s.transfers.Add(ctx, uuid.New(), transfer.NewTransfer{
		RecipientName:     "Recipient",
		RecipientPhone:    "+243811111111",
		RecipientOperator: transfer.OperatorOrangeMoney,
		FromCurrency:      transfer.CurrencyUSD,
		FromOperator:      transfer.OperatorOrangeMoney,
		FromAmount:        from,
		ToCurrency:        transfer.CurrencyUSD,
		ToAmount:          from,
		FeePctBps:         200,
		FeeAmount:         fee,
		HoldAmount:        hold,
	})
	if err != nil {
		t.Fatalf("creating transfer: %v", err)
	}

	offers, err := s.offers.OfferBatch(ctx, tr.ID,
		[]matching.ScoredAgent{{AgentProfileID: a.ID, Score: 90}}, time.Now().Add(2*time.Minute))
	if err != nil || len(offers) != 1 {
		t.Fatalf("offering transfer: %v (%d offers)", err, len(offers))
	}
	if _, err := s.offers.Accept(ctx, offers[0].ID, a.ID, a.AccountID); err != nil {
		t.Fatalf("accepting offer: %v", err)
	}
	if _, err := s.transfers.MarkClientPaid(ctx, tr.ID.String(), uuid.New(), nil); err != nil {
		t.Fatalf("marking client paid: %v", err)
	}

	return tr
}

// TestOpenDisputeFreezesTheHold is the core rule of the whole dispute
// path: the transfer freezes and the collateral STAYS held — it is what
// funds the fix, so nothing may release it before an admin rules.
func TestOpenDisputeFreezesTheHold(t *testing.T) {
	s := newStores(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	a := fundedAgent(ctx, t, s, 20_000)
	tr := fundedTransfer(ctx, t, s, a, 5_100)

	before := profile(ctx, t, s, a.ID)
	if before.Held != 5_100 {
		t.Fatalf("held before the dispute = %d, want 5100", before.Held)
	}

	opened, err := s.disputes.Open(ctx, dispute.NewDispute{
		TransferID: tr.ID,
		Party:      dispute.PartySystem,
		Reason:     dispute.ReasonAgentNotPaid,
	})
	if err != nil {
		t.Fatalf("opening dispute: %v", err)
	}

	// The snapshot is taken at freeze time, from the transfer.
	if opened.FromStatus != transfer.StatusPendingAgentTransfer {
		t.Fatalf("from_status = %s, want pending_agent_transfer", opened.FromStatus)
	}
	if opened.HoldAmount != 5_100 || opened.RefundAmount != tr.TotalAmount {
		t.Fatalf("snapshot = hold %d / refund %d, want 5100 / %d",
			opened.HoldAmount, opened.RefundAmount, tr.TotalAmount)
	}
	if opened.OpenedBy != nil {
		t.Fatalf("a system dispute has no author: %v", opened.OpenedBy)
	}

	frozen := readTransfer(ctx, t, s, tr.ID)
	if frozen.Status != transfer.StatusDisputed {
		t.Fatalf("transfer status = %s, want disputed", frozen.Status)
	}

	after := profile(ctx, t, s, a.ID)
	if after.Held != before.Held || after.Balance != before.Balance {
		t.Fatalf("the dispute moved money: held %d->%d, balance %d->%d",
			before.Held, after.Held, before.Balance, after.Balance)
	}

	// A disputed transfer is not disputable again.
	if _, err := s.disputes.Open(ctx, dispute.NewDispute{
		TransferID: tr.ID, Party: dispute.PartyClient, Reason: dispute.ReasonOther,
	}); !errors.Is(err, dispute.ErrNotDisputable) {
		t.Fatalf("second dispute = %v, want ErrNotDisputable", err)
	}
}

// TestResolveReleaseFreesTheGuarantee covers the three rulings that end
// without a balance movement.
func TestResolveReleaseFreesTheGuarantee(t *testing.T) {
	s := newStores(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	a := fundedAgent(ctx, t, s, 20_000)
	tr := fundedTransfer(ctx, t, s, a, 5_100)
	opened := open(ctx, t, s, tr.ID)

	effect, err := dispute.EffectOf(dispute.ResolutionAgentRight)
	if err != nil {
		t.Fatalf("EffectOf: %v", err)
	}

	admin := uuid.New()
	note := "payout proof verified with the operator"
	resolved, err := s.disputes.ResolveRelease(ctx, opened.ID.String(),
		dispute.ResolutionAgentRight, effect.TerminalStatus, admin, new(note))
	if err != nil {
		t.Fatalf("resolving: %v", err)
	}
	if resolved.Status != dispute.StatusResolved ||
		resolved.HoldOutcome == nil || *resolved.HoldOutcome != dispute.HoldReleased {
		t.Fatalf("resolved = %+v, want resolved/released", resolved)
	}
	if resolved.DeductionID != nil {
		t.Fatalf("a release must not create a deduction: %v", resolved.DeductionID)
	}

	if got := readTransfer(ctx, t, s, tr.ID); got.Status != transfer.StatusDone {
		t.Fatalf("transfer status = %s, want done", got.Status)
	}

	p := profile(ctx, t, s, a.ID)
	if p.Held != 0 {
		t.Fatalf("held = %d, want 0 (released)", p.Held)
	}
	if p.Balance != 20_000 {
		t.Fatalf("balance = %d, want 20000 (untouched)", p.Balance)
	}

	// Resolving twice is not a second release.
	if _, err := s.disputes.ResolveRelease(ctx, opened.ID.String(),
		dispute.ResolutionAgentRight, effect.TerminalStatus, admin, nil); !errors.Is(err, dispute.ErrDisputeNotFound) {
		t.Fatalf("second resolve = %v, want ErrDisputeNotFound", err)
	}
}

// TestResolveCaptureTakesTheGuarantee is the hold→balance crossover: the
// capture must move held AND balance by the same amount, through a
// settled deduction request with its own immutable ledger line.
func TestResolveCaptureTakesTheGuarantee(t *testing.T) {
	s := newStores(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	a := fundedAgent(ctx, t, s, 20_000)
	tr := fundedTransfer(ctx, t, s, a, 5_100)
	opened := open(ctx, t, s, tr.ID)

	admin := uuid.New()
	note := "recipient never paid, agent unreachable"
	resolved, err := s.disputes.ResolveCapture(ctx, opened.ID.String(), admin, new(note))
	if err != nil {
		t.Fatalf("capturing: %v", err)
	}
	if resolved.HoldOutcome == nil || *resolved.HoldOutcome != dispute.HoldCaptured {
		t.Fatalf("hold outcome = %v, want captured", resolved.HoldOutcome)
	}
	if resolved.Resolution == nil || *resolved.Resolution != dispute.ResolutionAgentFault {
		t.Fatalf("resolution = %v, want agent_fault", resolved.Resolution)
	}
	if resolved.DeductionID == nil {
		t.Fatal("a capture must name its deduction request")
	}

	if got := readTransfer(ctx, t, s, tr.ID); got.Status != transfer.StatusRefunded {
		t.Fatalf("transfer status = %s, want refunded", got.Status)
	}

	p := profile(ctx, t, s, a.ID)
	if p.Held != 0 {
		t.Fatalf("held = %d, want 0", p.Held)
	}
	if p.Balance != 20_000-5_100 {
		t.Fatalf("balance = %d, want %d (captured)", p.Balance, 20_000-5_100)
	}

	// The crossover went THROUGH a ledger request, settled, with a line.
	req, err := s.requests.Get(ctx, resolved.DeductionID.String())
	if err != nil {
		t.Fatalf("reading the deduction: %v", err)
	}
	if req.Type != ledger.TypeDeduction || req.Direction != ledger.DirectionOutflow ||
		req.Status != ledger.StatusDone || req.Amount != 5_100 {
		t.Fatalf("deduction = %+v, want a settled 5100 outflow", req)
	}

	lines, err := s.requests.ListLines(ctx, a.ID.String(), 10)
	if err != nil {
		t.Fatalf("listing lines: %v", err)
	}
	var capture *ledger.Line
	for i, l := range lines {
		if l.RequestID == req.ID {
			capture = &lines[i]
		}
	}
	if capture == nil {
		t.Fatal("the capture wrote no ledger line")
	}
	if capture.Amount != -5_100 || capture.BalanceAfter != 20_000-5_100 {
		t.Fatalf("line = %d (balance_after %d), want -5100 (%d)",
			capture.Amount, capture.BalanceAfter, 20_000-5_100)
	}
}

func open(ctx context.Context, t *testing.T, s stores, transferID uuid.UUID) dispute.Dispute {
	t.Helper()

	d, err := s.disputes.Open(ctx, dispute.NewDispute{
		TransferID: transferID,
		Party:      dispute.PartyClient,
		OpenedBy:   new(uuid.New()),
		Reason:     dispute.ReasonRecipientNotReceived,
	})
	if err != nil {
		t.Fatalf("opening dispute: %v", err)
	}
	return d
}

func profile(ctx context.Context, t *testing.T, s stores, id uuid.UUID) agentprofile.AgentProfile {
	t.Helper()

	p, err := s.profiles.Get(ctx, id.String())
	if err != nil {
		t.Fatalf("reading profile: %v", err)
	}
	return p
}

func readTransfer(ctx context.Context, t *testing.T, s stores, id uuid.UUID) transfer.Transfer {
	t.Helper()

	tr, err := s.transfers.Get(ctx, id.String())
	if err != nil {
		t.Fatalf("reading transfer: %v", err)
	}
	return tr
}
