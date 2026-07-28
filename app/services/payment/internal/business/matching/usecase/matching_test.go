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
)

// fakeJobs records every scheduled job/notification instead of touching
// River — the DB side of each usecase call still runs for real.
type fakeJobs struct {
	mu       sync.Mutex
	matches  []uuid.UUID
	expiries []uuid.UUID
	timeouts []uuid.UUID
	confirms []uuid.UUID
	notified []string // eventType
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

func (f *fakeJobs) ScheduleAutoConfirm(_ context.Context, _ pgx.Tx, id uuid.UUID, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.confirms = append(f.confirms, id)
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

type harness struct {
	transfers transferuc.UseCase
	matching  matchinguc.UseCase
	profiles  profilerepo.Store
	wallets   walletrepo.Store
	requests  ledgerrepo.Store
	jobs      *fakeJobs
	db        *coredb.Postgres
	log       *logger.Logger
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
	log := logger.New(os.Stderr, logger.LevelInfo, "test", nil)
	fx := fxrate.Static{Micros: 2_830_000_000}

	return harness{
		transfers: transferuc.New(log, db, fx, jobs, transferuc.Config{
			FeeSameBps:       150,
			FeeFXBps:         200,
			MarginBps:        50,
			MaxAmount:        1_000_000_000,
			AutoConfirmAfter: 24 * time.Hour,
		}),
		matching: matchinguc.New(log, db, jobs, matchinguc.Config{
			MaxAgents:      5,
			OfferTTL:       2 * time.Minute,
			MaxRounds:      10,
			MatchDeadline:  30 * time.Minute,
			PaymentTimeout: 24 * time.Hour,
		}),
		profiles: profilerepo.New(db),
		wallets:  walletrepo.New(db),
		requests: ledgerrepo.New(db),
		jobs:     jobs,
		db:       db,
		log:      log,
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

	// The stale auto-confirm timer no-ops.
	if err := h.transfers.HandleAutoConfirm(ctx, created.ID); err != nil {
		t.Fatalf("stale HandleAutoConfirm: %v", err)
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
	capUC := matchinguc.New(h.log, h.db, h.jobs, matchinguc.Config{
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

// ownerPassport fabricates the user passport the gateway would forward.
func ownerPassport(accountID uuid.UUID) *passport.Passport {
	return &passport.Passport{
		User: passport.User{ID: accountID.String(), Role: "user"},
		Type: passport.TypeUser,
	}
}
