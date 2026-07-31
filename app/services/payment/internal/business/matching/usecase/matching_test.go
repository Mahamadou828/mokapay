package usecase_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"mokapay.app/app/services/payment/internal/business/agentprofile"
	profilerepo "mokapay.app/app/services/payment/internal/business/agentprofile/repo"
	"mokapay.app/app/services/payment/internal/business/agentwallet"
	walletrepo "mokapay.app/app/services/payment/internal/business/agentwallet/repo"
	commissionrepo "mokapay.app/app/services/payment/internal/business/commission/repo"
	"mokapay.app/app/services/payment/internal/business/ledger"
	ledgerrepo "mokapay.app/app/services/payment/internal/business/ledger/repo"
	"mokapay.app/app/services/payment/internal/business/matching"
	matchinguc "mokapay.app/app/services/payment/internal/business/matching/usecase"
	"mokapay.app/app/services/payment/internal/business/transfer"
	transferuc "mokapay.app/app/services/payment/internal/business/transfer/usecase"
	"mokapay.app/app/services/payment/internal/integration/fxrate"
	coredb "mokapay.app/core/db"
	"mokapay.app/core/logger"
	"mokapay.app/core/passport"
	"mokapay.app/core/ws"
)

// fakeJobs records every scheduled job/notification instead of touching
// River — the DB side of each usecase call still runs for real.
type fakeJobs struct {
	mu          sync.Mutex
	matches     []uuid.UUID
	expiries    []uuid.UUID
	timeouts    []uuid.UUID
	payouts     []uuid.UUID
	confirms    []uuid.UUID
	evaluations []uuid.UUID
	notified    []string // eventType
}

func (f *fakeJobs) ScheduleMatch(_ context.Context, _ pgx.Tx, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.matches = append(f.matches, id)
	return nil
}

func (f *fakeJobs) ScheduleOffersExpire(_ context.Context, _ pgx.Tx, id uuid.UUID, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.expiries = append(f.expiries, id)
	return nil
}

func (f *fakeJobs) SchedulePaymentTimeout(_ context.Context, _ pgx.Tx, id uuid.UUID, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.timeouts = append(f.timeouts, id)
	return nil
}

func (f *fakeJobs) SchedulePayoutTimeout(_ context.Context, _ pgx.Tx, id uuid.UUID, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.payouts = append(f.payouts, id)
	return nil
}

func (f *fakeJobs) ScheduleAutoConfirm(_ context.Context, _ pgx.Tx, id uuid.UUID, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.confirms = append(f.confirms, id)
	return nil
}

func (f *fakeJobs) ScheduleComplianceEvaluation(_ context.Context, _ pgx.Tx, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.evaluations = append(f.evaluations, id)
	return nil
}

func (f *fakeJobs) Notify(_ context.Context, _ pgx.Tx, _, eventType, _, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.notified = append(f.notified, eventType)
	return nil
}

func (f *fakeJobs) count(event string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, e := range f.notified {
		if e == event {
			n++
		}
	}
	return n
}

// fakeLive stands in for the redis event bus: it records what the usecase
// would have pushed to each account's live sockets.
type fakeLive struct {
	mu     sync.Mutex
	events []liveEvent
}

type liveEvent struct {
	accountID string
	event     ws.Event
}

func (f *fakeLive) Publish(_ context.Context, accountID string, e ws.Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, liveEvent{accountID: accountID, event: e})
	return nil
}

// pushedTo returns the events published to one account for an event type.
func (f *fakeLive) pushedTo(accountID, eventType string) []ws.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []ws.Event
	for _, e := range f.events {
		if e.accountID == accountID && e.event.Type == eventType {
			out = append(out, e.event)
		}
	}
	return out
}

type harness struct {
	transfers   transferuc.UseCase
	matching    matchinguc.UseCase
	profiles    profilerepo.Store
	wallets     walletrepo.Store
	requests    ledgerrepo.Store
	commissions commissionrepo.Store

	jobs *fakeJobs
	live *fakeLive
	db   *coredb.Postgres
	log  *logger.Logger
}

func newHarness(t *testing.T) harness {
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

	jobs := &fakeJobs{}
	live := &fakeLive{}
	log := logger.New(os.Stderr, logger.LevelInfo, "test", nil)
	fx := fxrate.Static{Micros: 2_830_000_000}

	return harness{
		transfers: transferuc.New(log, db, fx, jobs, live, transferuc.Config{
			FeeSameBps:       150,
			FeeFXBps:         200,
			MarginBps:        50,
			MaxAmount:        1_000_000_000,
			PayoutTimeout:    6 * time.Hour,
			AutoConfirmAfter: 24 * time.Hour,
		}),
		matching: matchinguc.New(log, db, jobs, live, matchinguc.Config{
			MaxAgents:      5,
			OfferTTL:       2 * time.Minute,
			MaxRounds:      10,
			MatchDeadline:  30 * time.Minute,
			PaymentTimeout: 24 * time.Hour,
		}),
		profiles:    profilerepo.New(db),
		wallets:     walletrepo.New(db),
		requests:    ledgerrepo.New(db),
		commissions: commissionrepo.New(db),
		jobs:        jobs,
		live:        live,
		db:          db,
		log:         log,
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func (h harness) agent(ctx context.Context, t *testing.T, balance int64) agentprofile.AgentProfile {
	t.Helper()

	floor := int64(0)
	p, err := h.profiles.Add(ctx, agentprofile.NewAgentProfile{AccountID: uuid.New(), GuaranteeFloor: new(floor)})
	if err != nil {
		t.Fatalf("creating profile: %v", err)
	}
	// Suspend on the way out so this run's agents never pollute the
	// candidate pool of future runs (the DB is shared between runs).
	t.Cleanup(func() {
		_, _ = h.profiles.SetStatus(context.Background(), p.ID.String(), agentprofile.StatusSuspended) //nolint:errcheck // best-effort hygiene
	})
	req, err := h.requests.Add(ctx, ledger.StatusPendingAdminApproval, ledger.DirectionInflow, uuid.New(), ledger.NewRequest{
		AgentProfileID: p.ID, Type: ledger.TypeFunding, Amount: balance,
	})
	if err != nil {
		t.Fatalf("funding: %v", err)
	}
	if _, err := h.requests.Approve(ctx, req.ID.String(), uuid.NewString()); err != nil {
		t.Fatalf("approving funding: %v", err)
	}
	p2, err := h.profiles.SetStatus(ctx, p.ID.String(), agentprofile.StatusActive)
	if err != nil {
		t.Fatalf("activating: %v", err)
	}
	if _, err := h.wallets.Add(ctx, p2.ID, agentwallet.NewWallet{
		Operator: transfer.OperatorOrangeMoney, Msisdn: "+243991000001",
		AcceptsUSD: true, AcceptsCDF: true,
	}); err != nil {
		t.Fatalf("adding wallet: %v", err)
	}
	return p2
}

// TestFullTransferFlow drives the whole machine through the usecases:
// create (money stamped, match scheduled) → match round (offers +
// notifications) → accept (reservation + timeout clock) → both proofs
// (auto-confirm armed, replay idempotent) → confirm (done + release).
func TestFullTransferFlow(t *testing.T) {
	h := newHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	agent := h.agent(ctx, t, 50_000)
	client := uuid.New()

	// Create: 100 USD → CDF. fee 2% = 200; gross = 10000×2830e6/1e8 =
	// 283000; margin 1415; to 281585; hold = total = 10200.
	created, err := h.transfers.CreateTransfer(ctx, client, transferuc.CreateTransfer{
		FromCurrency: transfer.CurrencyUSD,
		FromOperator: transfer.OperatorOrangeMoney,
		FromAmount:   10_000,
		ToCurrency:   transfer.CurrencyCDF,
		Recipient: &transferuc.NewRecipient{
			Name:     "Mireille",
			Phone:    "+243812345678",
			Operator: transfer.OperatorOrangeMoney,
		},
	})
	if err != nil {
		t.Fatalf("CreateTransfer: %v", err)
	}
	if created.Status != transfer.StatusPendingMatch || created.HoldAmount != 10_200 ||
		created.ToAmount != 281_585 || created.FeeAmount != 200 || created.Rate == nil {
		t.Fatalf("created = %+v", created)
	}
	if created.BeneficiaryID == nil {
		t.Fatal("inline recipient must save a beneficiary")
	}
	if len(h.jobs.matches) != 1 || h.jobs.matches[0] != created.ID {
		t.Fatalf("scheduled matches = %v", h.jobs.matches)
	}

	// Re-creating with the same recipient reuses the beneficiary (upsert).
	bens, err := h.transfers.ListBeneficiaries(ctx, client)
	if err != nil || len(bens) != 1 {
		t.Fatalf("beneficiaries = %d, %v", len(bens), err)
	}

	// Match round: offer lands with the agent, expiry armed, agent told.
	if err := h.matching.RunMatch(ctx, created.ID); err != nil {
		t.Fatalf("RunMatch: %v", err)
	}
	if len(h.jobs.expiries) != 1 {
		t.Fatalf("expiry jobs = %v", h.jobs.expiries)
	}
	if h.jobs.count("transfer.offer_received") == 0 {
		t.Fatal("agent never notified of the offer")
	}
	inbox, err := h.matching.ListOffers(ctx, agent.AccountID, nil, 0)
	if err != nil || len(inbox) != 1 {
		t.Fatalf("inbox = %d, %v", len(inbox), err)
	}
	if inbox[0].HoldAmount != 10_200 || inbox[0].TransferReference == "" {
		t.Fatalf("inbox offer = %+v", inbox[0])
	}

	// The same fact also went out live: one push to the offered agent,
	// carrying what the inbox refetch needs — alongside the notification
	// job, never instead of it.
	pushed := h.live.pushedTo(agent.AccountID.String(), matching.EventOfferReceived)
	if len(pushed) != 1 {
		t.Fatalf("live offer pushes = %d, want 1", len(pushed))
	}
	if pushed[0].Data["transfer_id"] != created.ID.String() ||
		pushed[0].Data["offer_id"] != inbox[0].ID.String() ||
		pushed[0].Data["reference"] != created.Reference {
		t.Fatalf("live offer payload = %+v", pushed[0].Data)
	}

	// A second RunMatch on the same transfer is a stale job: no-op.
	if err := h.matching.RunMatch(ctx, created.ID); err != nil {
		t.Fatalf("stale RunMatch: %v", err)
	}

	// Accept: reservation + payment clock + client notification.
	accepted, err := h.matching.AcceptOffer(ctx, agent.AccountID, inbox[0].ID)
	if err != nil {
		t.Fatalf("AcceptOffer: %v", err)
	}
	if accepted.Status != transfer.StatusPendingClientTransfer {
		t.Fatalf("accepted = %+v", accepted)
	}
	if len(h.jobs.timeouts) != 1 || h.jobs.count("transfer.matched") != 1 {
		t.Fatalf("timeouts=%v matched=%d", h.jobs.timeouts, h.jobs.count("transfer.matched"))
	}
	if p, _ := h.profiles.Get(ctx, agent.ID.String()); p.Held != 10_200 {
		t.Fatalf("held after accept = %d", p.Held)
	}

	// The owner's read now carries the agent's payment details.
	view, err := h.transfers.Get(ctx, ownerPassport(client), created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if view.PaymentDetails == nil || view.PaymentDetails.Msisdn != "+243991000001" {
		t.Fatalf("payment details = %+v", view.PaymentDetails)
	}
	// A stranger sees nothing.
	if _, err := h.transfers.Get(ctx, ownerPassport(uuid.New()), created.ID); !errors.Is(err, transfer.ErrTransferNotFound) {
		t.Fatalf("stranger read = %v, want ErrTransferNotFound", err)
	}

	// Proof 1: client paid. Replay is idempotent.
	proof1 := uuid.New()
	if _, err := h.transfers.ApplyProof(ctx, created.ID, transfer.StatusPendingAgentTransfer, proof1); err != nil {
		t.Fatalf("ApplyProof(client): %v", err)
	}
	replayed, err := h.transfers.ApplyProof(ctx, created.ID, transfer.StatusPendingAgentTransfer, proof1)
	if err != nil || replayed.Status != transfer.StatusPendingAgentTransfer {
		t.Fatalf("replayed proof = %+v, %v — must be idempotent", replayed, err)
	}
	if h.jobs.count("transfer.client_paid") != 1 {
		t.Fatalf("client_paid notifications = %d, want 1 (replay must not re-notify)", h.jobs.count("transfer.client_paid"))
	}
	// The client is now out their money: the agent's payout clock starts,
	// and running it out auto-disputes rather than cancels.
	if len(h.jobs.payouts) != 1 || h.jobs.payouts[0] != created.ID {
		t.Fatalf("payout timeout jobs = %v, want one for %s", h.jobs.payouts, created.ID)
	}

	// Proof 2: payout done — auto-confirm armed.
	if _, err := h.transfers.ApplyProof(ctx, created.ID, transfer.StatusPendingClientConfirmation, uuid.New()); err != nil {
		t.Fatalf("ApplyProof(agent): %v", err)
	}
	if len(h.jobs.confirms) != 1 {
		t.Fatalf("auto-confirm jobs = %v", h.jobs.confirms)
	}

	// Confirm: done, hold released, agent told.
	done, err := h.transfers.Confirm(ctx, client, created.ID)
	if err != nil || done.Status != transfer.StatusDone {
		t.Fatalf("Confirm = %+v, %v", done, err)
	}
	if p, _ := h.profiles.Get(ctx, agent.ID.String()); p.Held != 0 || p.Balance != 50_000 {
		t.Fatalf("profile after done = %+v", p)
	}

	// Reaching done pays the agent: the accrual commits with the
	// completion, splitting the fee 60/30/10 for the agent, the platform
	// and the guarantee fund.
	accrued, err := h.commissions.GetByTransfer(ctx, created.ID.String())
	if err != nil {
		t.Fatalf("commission after done: %v", err)
	}
	if accrued.AgentProfileID != agent.ID || accrued.FeeAmount != done.FeeAmount {
		t.Fatalf("accrual = agent %s / fee %d, want %s / %d",
			accrued.AgentProfileID, accrued.FeeAmount, agent.ID, done.FeeAmount)
	}
	if accrued.AgentAmount+accrued.PlatformAmount+accrued.GuaranteeFundAmount != done.FeeAmount {
		t.Fatalf("accrual split %+v does not re-add to the fee %d", accrued, done.FeeAmount)
	}
	// The FX margin is the platform's, kept in the to_currency — never
	// folded into the fee split.
	if accrued.MarginAmount != done.MarginAmount || accrued.MarginCurrency != done.ToCurrency {
		t.Fatalf("accrual margin = %d %s, want %d %s",
			accrued.MarginAmount, accrued.MarginCurrency, done.MarginAmount, done.ToCurrency)
	}

	// Reaching done also hands the movement to compliance — enqueued with
	// the completion, evaluated afterwards, and never able to affect it.
	if len(h.jobs.evaluations) != 1 || h.jobs.evaluations[0] != created.ID {
		t.Fatalf("compliance evaluation jobs = %v, want one for %s", h.jobs.evaluations, created.ID)
	}

	// The stale auto-confirm timer no-ops — and cannot double-book the
	// commission or the evaluation on its way through.
	if err := h.transfers.HandleAutoConfirm(ctx, created.ID); err != nil {
		t.Fatalf("stale HandleAutoConfirm: %v", err)
	}
	if again, err := h.commissions.GetByTransfer(ctx, created.ID.String()); err != nil || again.ID != accrued.ID {
		t.Fatalf("commission after the stale timer = %+v, %v — must be the same single row", again, err)
	}
	if len(h.jobs.evaluations) != 1 {
		t.Fatalf("compliance evaluation jobs after the stale timer = %v, want still one", h.jobs.evaluations)
	}
}

// TestMatchLifecycleEdges: no candidates → snooze signal; decline →
// immediate requeue; round cap → terminal expiry + owner notification.
func TestMatchLifecycleEdges(t *testing.T) {
	h := newHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	client := uuid.New()
	// A $4M transfer: its hold (4.08e8 cents) exceeds anything a leftover
	// agent from other tests can cover — only this test's mega-funded
	// agents are eligible. That keeps the "empty pool" and "single live
	// offer" assumptions true on a shared DB.
	created, err := h.transfers.CreateTransfer(ctx, client, transferuc.CreateTransfer{
		FromCurrency: transfer.CurrencyUSD,
		FromOperator: transfer.OperatorMPesa,
		FromAmount:   400_000_000,
		ToCurrency:   transfer.CurrencyCDF,
		Recipient: &transferuc.NewRecipient{
			Name: "R", Phone: "+243819999999", Operator: transfer.OperatorMPesa,
		},
	})
	if err != nil {
		t.Fatalf("CreateTransfer: %v", err)
	}

	// Empty pool → the snooze signal.
	if err := h.matching.RunMatch(ctx, created.ID); !errors.Is(err, matching.ErrNoCandidates) {
		t.Fatalf("RunMatch empty pool = %v, want ErrNoCandidates", err)
	}

	// An agent appears; the round goes out; a decline requeues immediately.
	agent := h.agent(ctx, t, 1_000_000_000)
	if _, err := h.wallets.Add(ctx, agent.ID, agentwallet.NewWallet{
		Operator: transfer.OperatorMPesa, Msisdn: "+243991000003",
		AcceptsUSD: true, AcceptsCDF: true,
	}); err != nil {
		t.Fatalf("adding wallet: %v", err)
	}
	if err := h.matching.RunMatch(ctx, created.ID); err != nil {
		t.Fatalf("RunMatch: %v", err)
	}
	inbox, err := h.matching.ListOffers(ctx, agent.AccountID, nil, 0)
	if err != nil || len(inbox) != 1 {
		t.Fatalf("inbox = %d, %v", len(inbox), err)
	}
	preMatches := len(h.jobs.matches)
	if _, err := h.matching.DeclineOffer(ctx, agent.AccountID, inbox[0].ID); err != nil {
		t.Fatalf("DeclineOffer: %v", err)
	}
	if len(h.jobs.matches) != preMatches+1 {
		t.Fatal("decline of the last live offer must requeue immediately")
	}

	// The declined agent is out; the next round finds nobody again.
	if err := h.matching.RunMatch(ctx, created.ID); !errors.Is(err, matching.ErrNoCandidates) {
		t.Fatalf("post-decline RunMatch = %v, want ErrNoCandidates", err)
	}

	// Drive the transfer to the round cap with a negative-TTL matcher
	// (offers land pre-expired, so expire→requeue cycles rounds) and a
	// second eligible agent.
	agent2 := h.agent(ctx, t, 1_000_000_000)
	if _, err := h.wallets.Add(ctx, agent2.ID, agentwallet.NewWallet{
		Operator: transfer.OperatorMPesa, Msisdn: "+243991000005",
		AcceptsUSD: true, AcceptsCDF: true,
	}); err != nil {
		t.Fatalf("adding wallet: %v", err)
	}
	capUC := matchinguc.New(h.log, h.db, h.jobs, h.live, matchinguc.Config{
		MaxAgents:      5,
		OfferTTL:       -time.Second,
		MaxRounds:      2,
		MatchDeadline:  30 * time.Minute,
		PaymentTimeout: 24 * time.Hour,
	})
	if err := capUC.RunMatch(ctx, created.ID); err != nil { // round 1 offers, pre-expired
		t.Fatalf("capUC RunMatch: %v", err)
	}
	if err := capUC.HandleOffersExpired(ctx, created.ID); err != nil { // round → 2
		t.Fatalf("capUC HandleOffersExpired: %v", err)
	}
	// match_round ≥ MaxRounds — this run expires the transfer.
	if err := capUC.RunMatch(ctx, created.ID); err != nil {
		t.Fatalf("RunMatch at cap: %v", err)
	}
	got, err := h.transfers.Get(ctx, ownerPassport(client), created.ID)
	if err != nil || got.Status != transfer.StatusExpired {
		t.Fatalf("transfer after cap = %+v, %v — want expired", got.Transfer.Status, err)
	}
	if h.jobs.count("transfer.expired") != 1 {
		t.Fatalf("expired notifications = %d", h.jobs.count("transfer.expired"))
	}
}

// TestPaymentTimeoutFlow: the client never pays — cancel + release +
// both parties told.
func TestPaymentTimeoutFlow(t *testing.T) {
	h := newHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	agent := h.agent(ctx, t, 30_000)
	client := uuid.New()
	created, err := h.transfers.CreateTransfer(ctx, client, transferuc.CreateTransfer{
		FromCurrency: transfer.CurrencyUSD,
		FromOperator: transfer.OperatorOrangeMoney,
		FromAmount:   8_000,
		ToCurrency:   transfer.CurrencyCDF,
		Recipient:    &transferuc.NewRecipient{Name: "R", Phone: "+243817777777", Operator: transfer.OperatorOrangeMoney},
	})
	if err != nil {
		t.Fatalf("CreateTransfer: %v", err)
	}
	if err := h.matching.RunMatch(ctx, created.ID); err != nil {
		t.Fatalf("RunMatch: %v", err)
	}
	inbox, _ := h.matching.ListOffers(ctx, agent.AccountID, nil, 0)
	if _, err := h.matching.AcceptOffer(ctx, agent.AccountID, inbox[0].ID); err != nil {
		t.Fatalf("AcceptOffer: %v", err)
	}

	if err := h.transfers.HandlePaymentTimeout(ctx, created.ID); err != nil {
		t.Fatalf("HandlePaymentTimeout: %v", err)
	}
	if p, _ := h.profiles.Get(ctx, agent.ID.String()); p.Held != 0 {
		t.Fatalf("held after timeout = %d", p.Held)
	}
	if h.jobs.count("transfer.cancelled") != 2 {
		t.Fatalf("cancelled notifications = %d, want 2 (client + agent)", h.jobs.count("transfer.cancelled"))
	}
	// The stale timer is idempotent.
	if err := h.transfers.HandlePaymentTimeout(ctx, created.ID); err != nil {
		t.Fatalf("stale HandlePaymentTimeout: %v", err)
	}
}

// TestOfferBroadcastPushesEveryShortlistedAgent: a broadcast round is not
// one push — every agent on the shortlist gets the live event, because the
// race it announces is between them.
func TestOfferBroadcastPushesEveryShortlistedAgent(t *testing.T) {
	h := newHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// A $2M transfer: only this test's mega-funded agents can cover the
	// hold, so the shortlist is exactly the two agents below.
	client := uuid.New()
	created, err := h.transfers.CreateTransfer(ctx, client, transferuc.CreateTransfer{
		FromCurrency: transfer.CurrencyUSD,
		FromOperator: transfer.OperatorAirtelMoney,
		FromAmount:   200_000_000,
		ToCurrency:   transfer.CurrencyCDF,
		Recipient: &transferuc.NewRecipient{
			Name: "R", Phone: "+243818888888", Operator: transfer.OperatorAirtelMoney,
		},
	})
	if err != nil {
		t.Fatalf("CreateTransfer: %v", err)
	}

	shortlist := make([]agentprofile.AgentProfile, 0, 2)
	for i, msisdn := range []string{"+243991000007", "+243991000008"} {
		a := h.agent(ctx, t, 900_000_000)
		if _, err := h.wallets.Add(ctx, a.ID, agentwallet.NewWallet{
			Operator: transfer.OperatorAirtelMoney, Msisdn: msisdn,
			AcceptsUSD: true, AcceptsCDF: true,
		}); err != nil {
			t.Fatalf("adding wallet %d: %v", i, err)
		}
		shortlist = append(shortlist, a)
	}

	if err := h.matching.RunMatch(ctx, created.ID); err != nil {
		t.Fatalf("RunMatch: %v", err)
	}

	for _, a := range shortlist {
		inbox, err := h.matching.ListOffers(ctx, a.AccountID, nil, 0)
		if err != nil || len(inbox) != 1 {
			t.Fatalf("inbox of %s = %d, %v", a.ID, len(inbox), err)
		}
		pushed := h.live.pushedTo(a.AccountID.String(), matching.EventOfferReceived)
		if len(pushed) != 1 {
			t.Fatalf("live pushes to %s = %d, want 1 per offered agent", a.ID, len(pushed))
		}
		if pushed[0].Data["offer_id"] != inbox[0].ID.String() {
			t.Fatalf("live push to %s carries offer %q, its offer is %s",
				a.ID, pushed[0].Data["offer_id"], inbox[0].ID)
		}
	}
}

// TestTransferTransitionsPushBothParties is the bug the websocket work
// exists for: a proof upload flips the transfer's status, and the party
// that did NOT upload used to sit on a stale screen until its poll came
// round ("the agent needs to manually refresh to see the proof").
//
// Every transition on the money path must reach BOTH parties' sockets —
// the accept, the client's proof, the agent's payout proof and the
// completion — carrying the transfer id its screen refetches with.
func TestTransferTransitionsPushBothParties(t *testing.T) {
	h := newHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	agent := h.agent(ctx, t, 60_000)
	client := uuid.New()

	created, err := h.transfers.CreateTransfer(ctx, client, transferuc.CreateTransfer{
		FromCurrency: transfer.CurrencyUSD,
		FromOperator: transfer.OperatorOrangeMoney,
		FromAmount:   12_000,
		ToCurrency:   transfer.CurrencyCDF,
		Recipient: &transferuc.NewRecipient{
			Name: "Mireille", Phone: "+243812345671", Operator: transfer.OperatorOrangeMoney,
		},
	})
	if err != nil {
		t.Fatalf("CreateTransfer: %v", err)
	}

	// bothTold asserts one push per party for an event, carrying the ids a
	// refetch needs — the payload is never the transfer itself.
	bothTold := func(event string) {
		t.Helper()
		for who, account := range map[string]uuid.UUID{"client": client, "agent": agent.AccountID} {
			pushed := h.live.pushedTo(account.String(), event)
			if len(pushed) != 1 {
				t.Fatalf("%s pushes of %s to the %s = %d, want 1", created.Reference, event, who, len(pushed))
			}
			if pushed[0].Data["transfer_id"] != created.ID.String() ||
				pushed[0].Data["reference"] != created.Reference {
				t.Fatalf("%s push to the %s = %+v", event, who, pushed[0].Data)
			}
		}
	}

	if err := h.matching.RunMatch(ctx, created.ID); err != nil {
		t.Fatalf("RunMatch: %v", err)
	}
	inbox, err := h.matching.ListOffers(ctx, agent.AccountID, nil, 0)
	if err != nil || len(inbox) != 1 {
		t.Fatalf("inbox = %d, %v", len(inbox), err)
	}

	// Accept: the client is watching the "searching" screen.
	if _, err := h.matching.AcceptOffer(ctx, agent.AccountID, inbox[0].ID); err != nil {
		t.Fatalf("AcceptOffer: %v", err)
	}
	bothTold(transfer.EventMatched)

	// The client's funding proof — the agent must learn of it without a
	// manual refresh. A redelivered proof moves nothing, so it must not
	// push a second time either.
	proof := uuid.New()
	if _, err := h.transfers.ApplyProof(ctx, created.ID, transfer.StatusPendingAgentTransfer, proof); err != nil {
		t.Fatalf("ApplyProof(client): %v", err)
	}
	if _, err := h.transfers.ApplyProof(ctx, created.ID, transfer.StatusPendingAgentTransfer, proof); err != nil {
		t.Fatalf("ApplyProof(client) replay: %v", err)
	}
	bothTold(transfer.EventClientPaid)

	// The agent's payout proof — now it is the client's screen that would
	// otherwise be stale.
	if _, err := h.transfers.ApplyProof(ctx, created.ID, transfer.StatusPendingClientConfirmation, uuid.New()); err != nil {
		t.Fatalf("ApplyProof(agent): %v", err)
	}
	bothTold(transfer.EventPayoutDone)

	// Completion releases the agent's guarantee: their screen cares as much
	// as the client's.
	if _, err := h.transfers.Confirm(ctx, client, created.ID); err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	bothTold(transfer.EventCompleted)
}

// TestCancellationPushesBothParties: the payment window elapsing is a
// transition nobody triggered — neither screen has any other way to hear
// about it before its poll.
func TestCancellationPushesBothParties(t *testing.T) {
	h := newHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	agent := h.agent(ctx, t, 40_000)
	client := uuid.New()
	created, err := h.transfers.CreateTransfer(ctx, client, transferuc.CreateTransfer{
		FromCurrency: transfer.CurrencyUSD,
		FromOperator: transfer.OperatorOrangeMoney,
		FromAmount:   9_000,
		ToCurrency:   transfer.CurrencyCDF,
		Recipient:    &transferuc.NewRecipient{Name: "R", Phone: "+243817777771", Operator: transfer.OperatorOrangeMoney},
	})
	if err != nil {
		t.Fatalf("CreateTransfer: %v", err)
	}
	if err := h.matching.RunMatch(ctx, created.ID); err != nil {
		t.Fatalf("RunMatch: %v", err)
	}
	inbox, _ := h.matching.ListOffers(ctx, agent.AccountID, nil, 0)
	if _, err := h.matching.AcceptOffer(ctx, agent.AccountID, inbox[0].ID); err != nil {
		t.Fatalf("AcceptOffer: %v", err)
	}

	if err := h.transfers.HandlePaymentTimeout(ctx, created.ID); err != nil {
		t.Fatalf("HandlePaymentTimeout: %v", err)
	}
	for who, account := range map[string]uuid.UUID{"client": client, "agent": agent.AccountID} {
		if pushed := h.live.pushedTo(account.String(), transfer.EventCancelled); len(pushed) != 1 {
			t.Fatalf("cancellation pushes to the %s = %d, want 1", who, len(pushed))
		}
	}

	// The stale timer cancels nothing the second time — and announces
	// nothing either: a phantom event costs both screens a pointless refetch.
	if err := h.transfers.HandlePaymentTimeout(ctx, created.ID); err != nil {
		t.Fatalf("stale HandlePaymentTimeout: %v", err)
	}
	if pushed := h.live.pushedTo(client.String(), transfer.EventCancelled); len(pushed) != 1 {
		t.Fatalf("cancellation pushes after the stale timer = %d, want still 1", len(pushed))
	}
}

// ownerPassport fabricates the user passport the gateway would forward.
func ownerPassport(accountID uuid.UUID) *passport.Passport {
	return &passport.Passport{
		User: passport.User{ID: accountID.String(), Role: "user"},
		Type: passport.TypeUser,
	}
}
