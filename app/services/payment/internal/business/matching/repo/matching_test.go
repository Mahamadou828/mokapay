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
	"mokapay.app/app/services/payment/internal/business/agentwallet"
	walletrepo "mokapay.app/app/services/payment/internal/business/agentwallet/repo"
	"mokapay.app/app/services/payment/internal/business/ledger"
	ledgerrepo "mokapay.app/app/services/payment/internal/business/ledger/repo"
	"mokapay.app/app/services/payment/internal/business/matching"
	"mokapay.app/app/services/payment/internal/business/matching/repo"
	"mokapay.app/app/services/payment/internal/business/transfer"
	transferrepo "mokapay.app/app/services/payment/internal/business/transfer/repo"
	coredb "mokapay.app/core/db"
)

// Integration tests: need a migrated payments database. Skipped unless
// TEST_DB_PORT is set:
//
//	TEST_DB_PORT=5433 go test ./internal/business/...
type stores struct {
	offers    repo.Store
	transfers transferrepo.Store
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
		offers:    repo.New(db),
		transfers: transferrepo.New(db),
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

// fundedAgent builds an ACTIVE profile with the given balance and both
// wallet ends of the default test corridor (usd orange -> cdf orange).
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
	if balance > 0 {
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
	}
	p2, err := s.profiles.SetStatus(ctx, p.ID.String(), agentprofile.StatusActive)
	if err != nil {
		t.Fatalf("activating profile: %v", err)
	}

	for _, w := range []agentwallet.NewWallet{
		{Operator: transfer.OperatorOrangeMoney, Currency: transfer.CurrencyUSD, Msisdn: "+243990000001"},
		{Operator: transfer.OperatorOrangeMoney, Currency: transfer.CurrencyCDF, Msisdn: "+243990000002"},
	} {
		if _, err := s.wallets.Add(ctx, p2.ID, w); err != nil {
			t.Fatalf("adding wallet: %v", err)
		}
	}
	return p2
}

// usdToCdfTransfer creates a usd->cdf transfer whose hold is holdUSD.
func usdToCdfTransfer(ctx context.Context, t *testing.T, s stores, client uuid.UUID, holdUSD int64) transfer.Transfer {
	t.Helper()

	rate := int64(2_830_000_000) // 2830.000000 CDF per USD, micros
	// from_amount so that total (from+fee) == holdUSD: fee 2% => from = hold/1.02
	from := holdUSD * 100 / 102
	fee := holdUSD - from
	tr, err := s.transfers.Add(ctx, client, transfer.NewTransfer{
		RecipientName:     "Recipient",
		RecipientPhone:    "+243811111111",
		RecipientOperator: transfer.OperatorOrangeMoney,
		FromCurrency:      transfer.CurrencyUSD,
		FromOperator:      transfer.OperatorOrangeMoney,
		FromAmount:        from,
		ToCurrency:        transfer.CurrencyCDF,
		ToAmount:          from * 2830 / 100, // cents -> whole francs at 2830
		FeePctBps:         200,
		FeeAmount:         fee,
		MarginAmount:      100,
		HoldAmount:        holdUSD,
		RateMicros:        new(rate),
	})
	if err != nil {
		t.Fatalf("creating transfer: %v", err)
	}
	return tr
}

func offerTo(ctx context.Context, t *testing.T, s stores, transferID uuid.UUID, agents ...uuid.UUID) []matching.Offer {
	t.Helper()

	scored := make([]matching.ScoredAgent, 0, len(agents))
	for _, a := range agents {
		scored = append(scored, matching.ScoredAgent{AgentProfileID: a, Score: 80})
	}
	offers, err := s.offers.OfferBatch(ctx, transferID, scored, time.Now().Add(2*time.Minute))
	if err != nil {
		t.Fatalf("offering transfer: %v", err)
	}
	if len(offers) != len(agents) {
		t.Fatalf("offer batch = %d rows, want %d", len(offers), len(agents))
	}
	return offers
}

// TestAcceptRace: N agents race for one transfer — exactly one wins,
// exactly one reservation is placed, siblings are voided.
func TestAcceptRace(t *testing.T) {
	s := newStores(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	const racers = 6
	agents := make([]agentprofile.AgentProfile, racers)
	ids := make([]uuid.UUID, racers)
	for i := range agents {
		agents[i] = fundedAgent(ctx, t, s, 10_000)
		ids[i] = agents[i].ID
	}
	tr := usdToCdfTransfer(ctx, t, s, uuid.New(), 5_000)
	offers := offerTo(ctx, t, s, tr.ID, ids...)

	var wg sync.WaitGroup
	wins := make(chan uuid.UUID, racers)
	for i := range offers {
		wg.Add(1)
		go func(o matching.Offer, agent agentprofile.AgentProfile) {
			defer wg.Done()
			if _, err := s.offers.Accept(ctx, o.ID, agent.ID, agent.AccountID); err == nil {
				wins <- agent.ID
			} else if !errors.Is(err, matching.ErrOfferGone) {
				t.Errorf("unexpected accept error: %v", err)
			}
		}(offers[i], agents[i])
	}
	wg.Wait()
	close(wins)

	var winners []uuid.UUID
	for w := range wins {
		winners = append(winners, w)
	}
	if len(winners) != 1 {
		t.Fatalf("winners = %d, want exactly 1", len(winners))
	}

	// Exactly one reservation across the fleet.
	var heldTotal int64
	for _, a := range agents {
		p, err := s.profiles.Get(ctx, a.ID.String())
		if err != nil {
			t.Fatalf("reading profile: %v", err)
		}
		heldTotal += p.Held
		if p.ID == winners[0] && p.Held != 5_000 {
			t.Errorf("winner held = %d, want 5000", p.Held)
		}
	}
	if heldTotal != 5_000 {
		t.Fatalf("total held across agents = %d, want 5000", heldTotal)
	}

	// Transfer assigned to the winner; sibling offers voided.
	got, err := s.transfers.Get(ctx, tr.ID.String())
	if err != nil {
		t.Fatalf("reading transfer: %v", err)
	}
	if got.Status != transfer.StatusPendingClientTransfer || got.AgentProfileID == nil || *got.AgentProfileID != winners[0] {
		t.Fatalf("transfer after race = %+v", got)
	}
	for _, o := range offers {
		cur, err := s.offers.Get(ctx, o.ID.String())
		if err != nil {
			t.Fatalf("reading offer: %v", err)
		}
		if cur.AgentProfileID == winners[0] {
			if cur.Status != matching.OfferStatusAccepted {
				t.Errorf("winner offer status = %s", cur.Status)
			}
		} else if cur.Status != matching.OfferStatusVoided {
			t.Errorf("loser offer status = %s, want voided", cur.Status)
		}
	}
}

// TestAcceptInsufficientAvailable: a failed guard burns NOTHING — the
// offer stays live, no reservation moves.
func TestAcceptInsufficientAvailable(t *testing.T) {
	s := newStores(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	poor := fundedAgent(ctx, t, s, 1_000) // can't cover a 5000 hold
	tr := usdToCdfTransfer(ctx, t, s, uuid.New(), 5_000)
	offers := offerTo(ctx, t, s, tr.ID, poor.ID)

	if _, err := s.offers.Accept(ctx, offers[0].ID, poor.ID, poor.AccountID); !errors.Is(err, matching.ErrOfferGone) {
		t.Fatalf("accept error = %v, want ErrOfferGone", err)
	}

	cur, err := s.offers.Get(ctx, offers[0].ID.String())
	if err != nil || cur.Status != matching.OfferStatusOffered {
		t.Fatalf("offer after failed accept = %+v, %v — must stay live", cur, err)
	}
	p, err := s.profiles.Get(ctx, poor.ID.String())
	if err != nil || p.Held != 0 {
		t.Fatalf("profile after failed accept = held %d, %v — nothing may be reserved", p.Held, err)
	}
	got, _ := s.transfers.Get(ctx, tr.ID.String())
	if got.Status != transfer.StatusPendingAgentApproval {
		t.Fatalf("transfer status = %s, want pending_agent_approval", got.Status)
	}
}

// TestCompleteReleasesExactlyOnce: done releases the hold; a second
// confirm (or the auto-confirm racing the manual one) is zero rows.
func TestCompleteReleasesExactlyOnce(t *testing.T) {
	s := newStores(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	agent := fundedAgent(ctx, t, s, 10_000)
	client := uuid.New()
	tr := usdToCdfTransfer(ctx, t, s, client, 4_000)
	offers := offerTo(ctx, t, s, tr.ID, agent.ID)
	if _, err := s.offers.Accept(ctx, offers[0].ID, agent.ID, agent.AccountID); err != nil {
		t.Fatalf("accept: %v", err)
	}

	proof1, proof2 := uuid.New(), uuid.New()
	if _, err := s.transfers.MarkClientPaid(ctx, tr.ID.String(), proof1, new(client)); err != nil {
		t.Fatalf("MarkClientPaid: %v", err)
	}
	// Redelivered proof event = zero rows, idempotent.
	if _, err := s.transfers.MarkClientPaid(ctx, tr.ID.String(), proof1, new(client)); !errors.Is(err, transfer.ErrTransferNotFound) {
		t.Fatalf("replayed MarkClientPaid = %v, want ErrTransferNotFound", err)
	}
	if _, err := s.transfers.MarkAgentPaid(ctx, tr.ID.String(), proof2, new(agent.AccountID)); err != nil {
		t.Fatalf("MarkAgentPaid: %v", err)
	}

	// 8 concurrent completes — exactly one succeeds.
	var wg sync.WaitGroup
	successes := make(chan struct{}, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.transfers.Complete(ctx, tr.ID.String(), new(client), nil); err == nil {
				successes <- struct{}{}
			} else if !errors.Is(err, transfer.ErrTransferNotFound) {
				t.Errorf("unexpected complete error: %v", err)
			}
		}()
	}
	wg.Wait()
	close(successes)
	if n := len(successes); n != 1 {
		t.Fatalf("successful completes = %d, want exactly 1", n)
	}

	p, err := s.profiles.Get(ctx, agent.ID.String())
	if err != nil || p.Held != 0 || p.Balance != 10_000 {
		t.Fatalf("profile after done = held %d balance %d, %v — hold must be released exactly once", p.Held, p.Balance, err)
	}

	got, _ := s.transfers.Get(ctx, tr.ID.String())
	if got.Status != transfer.StatusDone || got.ProofClientID == nil || got.ProofAgentID == nil {
		t.Fatalf("transfer after done = %+v", got)
	}

	// Timeline: create + offer + accept + 2 proofs + done = 6 rows.
	events, err := s.transfers.ListEvents(ctx, tr.ID.String())
	if err != nil || len(events) != 6 {
		t.Fatalf("timeline = %d rows (%v), want 6", len(events), err)
	}
	if events[0].FromStatus != nil || events[0].ToStatus != transfer.StatusPendingMatch {
		t.Fatalf("birth event = %+v", events[0])
	}
	last := events[len(events)-1]
	if last.ToStatus != transfer.StatusDone {
		t.Fatalf("last event = %+v", last)
	}
}

// TestDeclinedAgentExcluded: a decline takes the agent out of the
// pre-filter for this transfer forever; expiry does not.
func TestDeclinedAgentExcluded(t *testing.T) {
	s := newStores(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	agent := fundedAgent(ctx, t, s, 10_000)
	client := uuid.New()
	tr := usdToCdfTransfer(ctx, t, s, client, 2_000)

	q := matching.CandidateQuery{
		TransferID:   tr.ID,
		ClientID:     client,
		HoldAmount:   tr.HoldAmount,
		FromOperator: tr.FromOperator,
		FromCurrency: tr.FromCurrency,
		ToOperator:   tr.RecipientOperator,
		ToCurrency:   tr.ToCurrency,
		Limit:        50,
	}

	inList := func() bool {
		cands, err := s.offers.Candidates(ctx, q)
		if err != nil {
			t.Fatalf("candidates: %v", err)
		}
		for _, c := range cands {
			if c.AgentProfileID == agent.ID {
				return true
			}
		}
		return false
	}

	if !inList() {
		t.Fatal("agent missing from candidates before any offer")
	}

	offers := offerTo(ctx, t, s, tr.ID, agent.ID)
	if _, err := s.offers.Decline(ctx, offers[0].ID, agent.ID); err != nil {
		t.Fatalf("decline: %v", err)
	}
	if inList() {
		t.Fatal("declined agent still in candidates")
	}

	// The round is dead → requeue works (no live offers left).
	requeued, err := s.offers.Requeue(ctx, tr.ID)
	if err != nil {
		t.Fatalf("requeue: %v", err)
	}
	if requeued.Status != transfer.StatusPendingMatch || requeued.MatchRound != 1 {
		t.Fatalf("requeued = status %s round %d", requeued.Status, requeued.MatchRound)
	}

	// The client can never be their own agent.
	selfAgent := fundedAgent(ctx, t, s, 10_000)
	q2 := q
	q2.ClientID = selfAgent.AccountID
	cands, err := s.offers.Candidates(ctx, q2)
	if err != nil {
		t.Fatalf("candidates: %v", err)
	}
	for _, c := range cands {
		if c.AgentProfileID == selfAgent.ID {
			t.Fatal("self-deal guard failed: client offered their own transfer")
		}
	}
}

// TestCancelPathsReleaseHold: timeout-cancel and admin-cancel both
// release; pre-accept client cancel has nothing to release and is
// blocked post-accept.
func TestCancelPathsReleaseHold(t *testing.T) {
	s := newStores(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	client := uuid.New()

	// Client cancel pre-accept: fine, offers voided.
	tr1 := usdToCdfTransfer(ctx, t, s, client, 2_000)
	a1 := fundedAgent(ctx, t, s, 10_000)
	off1 := offerTo(ctx, t, s, tr1.ID, a1.ID)
	cancelled, err := s.transfers.CancelPreAccept(ctx, tr1.ID.String(), client, nil)
	if err != nil || cancelled.Status != transfer.StatusCancelled {
		t.Fatalf("pre-accept cancel = %+v, %v", cancelled, err)
	}
	if o, _ := s.offers.Get(ctx, off1[0].ID.String()); o.Status != matching.OfferStatusVoided {
		t.Fatalf("offer after cancel = %s, want voided", o.Status)
	}

	// Post-accept: client cancel is impossible; timeout releases.
	tr2 := usdToCdfTransfer(ctx, t, s, client, 3_000)
	a2 := fundedAgent(ctx, t, s, 10_000)
	off2 := offerTo(ctx, t, s, tr2.ID, a2.ID)
	if _, err := s.offers.Accept(ctx, off2[0].ID, a2.ID, a2.AccountID); err != nil {
		t.Fatalf("accept: %v", err)
	}
	if _, err := s.transfers.CancelPreAccept(ctx, tr2.ID.String(), client, nil); !errors.Is(err, transfer.ErrTransferNotFound) {
		t.Fatalf("post-accept client cancel = %v, want ErrTransferNotFound", err)
	}
	if _, err := s.transfers.CancelTimeout(ctx, tr2.ID.String(), "payment window elapsed"); err != nil {
		t.Fatalf("timeout cancel: %v", err)
	}
	if p, _ := s.profiles.Get(ctx, a2.ID.String()); p.Held != 0 {
		t.Fatalf("held after timeout cancel = %d, want 0", p.Held)
	}

	// Admin cancel mid-flow releases too.
	tr3 := usdToCdfTransfer(ctx, t, s, client, 3_000)
	a3 := fundedAgent(ctx, t, s, 10_000)
	off3 := offerTo(ctx, t, s, tr3.ID, a3.ID)
	if _, err := s.offers.Accept(ctx, off3[0].ID, a3.ID, a3.AccountID); err != nil {
		t.Fatalf("accept: %v", err)
	}
	if _, err := s.transfers.MarkClientPaid(ctx, tr3.ID.String(), uuid.New(), new(client)); err != nil {
		t.Fatalf("MarkClientPaid: %v", err)
	}
	if _, err := s.transfers.CancelAdmin(ctx, tr3.ID.String(), uuid.New(), "ops intervention"); err != nil {
		t.Fatalf("admin cancel: %v", err)
	}
	if p, _ := s.profiles.Get(ctx, a3.ID.String()); p.Held != 0 {
		t.Fatalf("held after admin cancel = %d, want 0", p.Held)
	}
	// Terminal: a second admin cancel is zero rows.
	if _, err := s.transfers.CancelAdmin(ctx, tr3.ID.String(), uuid.New(), "again"); !errors.Is(err, transfer.ErrTransferNotFound) {
		t.Fatalf("double admin cancel = %v, want ErrTransferNotFound", err)
	}
}

// TestOfferExpiryFlow: expire due offers -> requeue -> re-offer works;
// expire_transfer voids leftovers and is terminal.
func TestOfferExpiryFlow(t *testing.T) {
	s := newStores(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	agent := fundedAgent(ctx, t, s, 10_000)
	client := uuid.New()
	tr := usdToCdfTransfer(ctx, t, s, client, 2_000)

	// A batch that expires immediately.
	scored := []matching.ScoredAgent{{AgentProfileID: agent.ID, Score: 50}}
	if _, err := s.offers.OfferBatch(ctx, tr.ID, scored, time.Now().Add(-time.Second)); err != nil {
		t.Fatalf("offer batch: %v", err)
	}

	expired, err := s.offers.ExpireDue(ctx, tr.ID)
	if err != nil || len(expired) != 1 {
		t.Fatalf("expire due = %d rows, %v", len(expired), err)
	}
	// The expired agent may be re-offered (only declines exclude).
	cands, err := s.offers.Candidates(ctx, matching.CandidateQuery{
		TransferID: tr.ID, ClientID: client, HoldAmount: tr.HoldAmount,
		FromOperator: tr.FromOperator, FromCurrency: tr.FromCurrency,
		ToOperator: tr.RecipientOperator, ToCurrency: tr.ToCurrency, Limit: 50,
	})
	if err != nil {
		t.Fatalf("candidates: %v", err)
	}
	found := false
	for _, c := range cands {
		found = found || c.AgentProfileID == agent.ID
	}
	if !found {
		t.Fatal("expired agent excluded from candidates — only declines should exclude")
	}

	if _, err := s.offers.Requeue(ctx, tr.ID); err != nil {
		t.Fatalf("requeue: %v", err)
	}

	// Deadline hit → terminal expiry.
	if _, err := s.transfers.Expire(ctx, tr.ID.String(), "match deadline exceeded"); err != nil {
		t.Fatalf("expire transfer: %v", err)
	}
	got, _ := s.transfers.Get(ctx, tr.ID.String())
	if got.Status != transfer.StatusExpired {
		t.Fatalf("status = %s, want expired", got.Status)
	}
	// offer_transfer on a terminal transfer = zero offers.
	batch, err := s.offers.OfferBatch(ctx, tr.ID, scored, time.Now().Add(time.Minute))
	if err != nil || len(batch) != 0 {
		t.Fatalf("offer batch on expired transfer = %d rows, %v — want 0", len(batch), err)
	}
}

// TestCorridorAndWalletGates: the CDF->USD CHECK and the wallet-fit gate.
func TestCorridorAndWalletGates(t *testing.T) {
	s := newStores(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// CDF -> USD aborts on the corridor CHECK.
	rate := int64(2_830_000_000)
	_, err := s.transfers.Add(ctx, uuid.New(), transfer.NewTransfer{
		RecipientName: "R", RecipientPhone: "+243812222222",
		RecipientOperator: transfer.OperatorMPesa,
		FromCurrency:      transfer.CurrencyCDF,
		FromOperator:      transfer.OperatorMPesa,
		FromAmount:        1000, ToCurrency: transfer.CurrencyUSD, ToAmount: 100,
		FeePctBps: 150, FeeAmount: 15, HoldAmount: 100, RateMicros: new(rate),
	})
	if err == nil {
		t.Fatal("cdf->usd transfer created — the corridor CHECK must reject it")
	}

	// Wallet fit: an agent with only the USD side never matches usd->cdf.
	floor := int64(0)
	p, err := s.profiles.Add(ctx, agentprofile.NewAgentProfile{AccountID: uuid.New(), GuaranteeFloor: new(floor)})
	if err != nil {
		t.Fatalf("profile: %v", err)
	}
	req, err := s.requests.Add(ctx, ledger.StatusPendingAdminApproval, ledger.DirectionInflow, uuid.New(), ledger.NewRequest{
		AgentProfileID: p.ID, Type: ledger.TypeFunding, Amount: 10_000,
	})
	if err != nil {
		t.Fatalf("fund: %v", err)
	}
	if _, err := s.requests.Approve(ctx, req.ID.String(), uuid.NewString()); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if _, err := s.profiles.SetStatus(ctx, p.ID.String(), agentprofile.StatusActive); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if _, err := s.wallets.Add(ctx, p.ID, agentwallet.NewWallet{
		Operator: transfer.OperatorOrangeMoney, Currency: transfer.CurrencyUSD, Msisdn: "+243990000009",
	}); err != nil {
		t.Fatalf("wallet: %v", err)
	}
	// Duplicate pair rejected.
	if _, err := s.wallets.Add(ctx, p.ID, agentwallet.NewWallet{
		Operator: transfer.OperatorOrangeMoney, Currency: transfer.CurrencyUSD, Msisdn: "+243990000010",
	}); !errors.Is(err, agentwallet.ErrWalletExists) {
		t.Fatalf("duplicate wallet = %v, want ErrWalletExists", err)
	}

	client := uuid.New()
	tr := usdToCdfTransfer(ctx, t, s, client, 2_000)
	cands, err := s.offers.Candidates(ctx, matching.CandidateQuery{
		TransferID: tr.ID, ClientID: client, HoldAmount: tr.HoldAmount,
		FromOperator: tr.FromOperator, FromCurrency: tr.FromCurrency,
		ToOperator: tr.RecipientOperator, ToCurrency: tr.ToCurrency, Limit: 50,
	})
	if err != nil {
		t.Fatalf("candidates: %v", err)
	}
	for _, c := range cands {
		if c.AgentProfileID == p.ID {
			t.Fatal("agent without the CDF wallet end matched a usd->cdf transfer")
		}
	}
}
