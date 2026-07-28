package usecase_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"mokapay.app/app/services/payment/internal/business/agentprofile"
	profilerepo "mokapay.app/app/services/payment/internal/business/agentprofile/repo"
	"mokapay.app/app/services/payment/internal/business/compliance"
	"mokapay.app/app/services/payment/internal/business/compliance/usecase"
	"mokapay.app/app/services/payment/internal/business/transfer"
	transferrepo "mokapay.app/app/services/payment/internal/business/transfer/repo"
	coredb "mokapay.app/core/db"
	"mokapay.app/core/logger"
)

// Integration tests: need a migrated payments database (TEST_DB_PORT). The
// user service is stubbed — account age is the one input payment does not
// own, and the interface exists precisely so this stays a one-service test.

const timezone = "Africa/Kinshasa"

// fakeAccounts answers account ages from a table, and errors for anything
// it was not told about.
type fakeAccounts struct {
	createdAt map[uuid.UUID]time.Time
	err       error
	calls     int
}

func (f *fakeAccounts) CreatedAt(_ context.Context, accountID uuid.UUID) (time.Time, error) {
	f.calls++
	if f.err != nil {
		return time.Time{}, f.err
	}
	at, ok := f.createdAt[accountID]
	if !ok {
		return time.Time{}, fmt.Errorf("account %s: %w", accountID, compliance.ErrAccountNotFound)
	}
	return at, nil
}

type fixture struct {
	db        *coredb.Postgres
	transfers transferrepo.Store
	profiles  profilerepo.Store
	accounts  *fakeAccounts
	uc        usecase.UseCase
}

func newFixture(t *testing.T) fixture {
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

	accounts := &fakeAccounts{createdAt: map[uuid.UUID]time.Time{}}
	log := logger.New(io.Discard, logger.LevelInfo, "test", nil)

	return fixture{
		db:        db,
		transfers: transferrepo.New(db),
		profiles:  profilerepo.New(db),
		accounts:  accounts,
		uc:        usecase.New(log, db, accounts, usecase.Config{Timezone: timezone}),
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func phone() string {
	return fmt.Sprintf("+2438%08d", rand.IntN(100_000_000))
}

func send(ctx context.Context, t *testing.T, f fixture, clientID uuid.UUID, to string, from int64) transfer.Transfer {
	t.Helper()

	fee := (from*150 + 5_000) / 10_000
	tr, err := f.transfers.Add(ctx, clientID, transfer.NewTransfer{
		RecipientName:     "Recipient",
		RecipientPhone:    to,
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

func TestSnapshotWithAgent(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	client := uuid.New()
	agentAccount := uuid.New()
	profile, err := f.profiles.Add(ctx, agentprofile.NewAgentProfile{AccountID: agentAccount})
	if err != nil {
		t.Fatalf("creating profile: %v", err)
	}
	f.accounts.createdAt[client] = time.Now().AddDate(0, 0, -10)
	f.accounts.createdAt[agentAccount] = time.Now().AddDate(0, 0, -400)

	to := phone()
	tr := send(ctx, t, f, client, to, 100_000) // $1000.00 → hold $1015.00
	if _, err := f.db.PgxPool.Exec(ctx,
		`UPDATE transfers SET agent_profile_fk = $1 WHERE id = $2`, profile.ID, tr.ID); err != nil {
		t.Fatalf("assigning agent: %v", err)
	}

	snap, err := f.uc.Snapshot(ctx, tr.ID)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	if snap.TransferID != tr.ID {
		t.Errorf("transfer id = %s, want %s", snap.TransferID, tr.ID)
	}
	if snap.Sender.AccountID != client {
		t.Errorf("sender = %s, want %s", snap.Sender.AccountID, client)
	}
	// Whole dollars: the hold is 101500 cents.
	if snap.Sender.CurrentAmountUSD != 1_015 {
		t.Errorf("sender current amount = %d, want 1015", snap.Sender.CurrentAmountUSD)
	}
	if snap.Sender.Rolling24hVolumeUSD != 1_015 {
		t.Errorf("sender 24h volume = %d, want 1015", snap.Sender.Rolling24hVolumeUSD)
	}
	if snap.Sender.AccountAgeDays != 10 {
		t.Errorf("sender age = %d, want 10", snap.Sender.AccountAgeDays)
	}

	if snap.Agent == nil {
		t.Fatal("agent metrics missing on a matched transfer")
	}
	// The agent travels as their USER ACCOUNT id: compliance keys its
	// audit rows on accounts, and the payment-local profile id would be a
	// meaningless subject there.
	if snap.Agent.AccountID != agentAccount {
		t.Errorf("agent = %s, want the account %s (not the profile %s)",
			snap.Agent.AccountID, agentAccount, profile.ID)
	}
	if snap.Agent.Rolling24hVolumeUSD != 1_015 {
		t.Errorf("agent 24h volume = %d, want 1015", snap.Agent.Rolling24hVolumeUSD)
	}
	if snap.Agent.AccountAgeDays != 400 {
		t.Errorf("agent age = %d, want 400", snap.Agent.AccountAgeDays)
	}

	if snap.Recipient.Phone != to {
		t.Errorf("recipient phone = %s, want %s", snap.Recipient.Phone, to)
	}
	if snap.Recipient.DistinctSenders7d != 1 {
		t.Errorf("distinct senders = %d, want 1", snap.Recipient.DistinctSenders7d)
	}
}

func TestSnapshotWithoutAgent(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	client := uuid.New()
	f.accounts.createdAt[client] = time.Now()

	tr := send(ctx, t, f, client, phone(), 5_000)

	snap, err := f.uc.Snapshot(ctx, tr.ID)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if snap.Agent != nil {
		t.Errorf("agent metrics = %+v on an unmatched transfer, want none", *snap.Agent)
	}
	// Only the sender was looked up — no agent, no second round trip.
	if f.accounts.calls != 1 {
		t.Errorf("account lookups = %d, want 1", f.accounts.calls)
	}
	if snap.Sender.AccountAgeDays != 0 {
		t.Errorf("sender age = %d, want 0 for an account opened today", snap.Sender.AccountAgeDays)
	}
}

func TestSnapshotFailsWhenAccountAgeIsUnknown(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	// The user service is unreachable. The snapshot must fail rather than
	// ship an age of zero: zero means "opened today", which trips the
	// new-account rule on an account of any age.
	f.accounts.err = errors.New("user service unavailable")

	tr := send(ctx, t, f, uuid.New(), phone(), 5_000)

	if _, err := f.uc.Snapshot(ctx, tr.ID); err == nil {
		t.Fatal("snapshot succeeded without an account age")
	}
}
