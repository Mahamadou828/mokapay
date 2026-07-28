package repo_test

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"mokapay.app/app/services/payment/internal/business/agentprofile"
	profilerepo "mokapay.app/app/services/payment/internal/business/agentprofile/repo"
	"mokapay.app/app/services/payment/internal/business/compliance"
	"mokapay.app/app/services/payment/internal/business/compliance/repo"
	"mokapay.app/app/services/payment/internal/business/transfer"
	transferrepo "mokapay.app/app/services/payment/internal/business/transfer/repo"
	coredb "mokapay.app/core/db"
)

// Integration tests: need a migrated payments database. Skipped unless
// TEST_DB_PORT is set:
//
//	TEST_DB_PORT=5433 go test ./internal/business/...
//
// Every case scopes itself to a freshly generated client / agent / phone,
// so the aggregates are unaffected by whatever else the database holds.

// timezone matches the service default — the night-time window is read in
// the market's clock (Africa/Kinshasa is UTC+1, no DST).
const timezone = "Africa/Kinshasa"

type stores struct {
	db        *coredb.Postgres
	metrics   repo.Store
	transfers transferrepo.Store
	profiles  profilerepo.Store
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
		db:        db,
		metrics:   repo.New(db),
		transfers: transferrepo.New(db),
		profiles:  profilerepo.New(db),
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// phone returns a recipient number no other test run shares.
func phone() string {
	return fmt.Sprintf("+2438%08d", rand.IntN(100_000_000))
}

// send creates a usd→usd transfer of `from` cents from the client. The
// hold is the client's full total (amount + fee), which is the column
// every volume aggregate sums.
func send(ctx context.Context, t *testing.T, s stores, clientID uuid.UUID, to string, from int64) transfer.Transfer {
	t.Helper()

	fee := (from*150 + 5_000) / 10_000
	tr, err := s.transfers.Add(ctx, clientID, transfer.NewTransfer{
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

// backdate moves a transfer's creation instant. The windows are all
// relative to now(), so a test that wants a 3-day-old transfer has to
// write one — there is no create-with-a-date path, by design.
func backdate(ctx context.Context, t *testing.T, s stores, id uuid.UUID, at time.Time) {
	t.Helper()

	if _, err := s.db.PgxPool.Exec(ctx,
		`UPDATE transfers SET created_at = $1 WHERE id = $2`, at, id); err != nil {
		t.Fatalf("backdating transfer: %v", err)
	}
}

// assign attaches an agent profile to a transfer. The real path is the
// accept procedure (offers, holds, guarantees) — none of which the
// aggregation looks at, so the column is set directly.
func assign(ctx context.Context, t *testing.T, s stores, id, agentProfileID uuid.UUID) {
	t.Helper()

	if _, err := s.db.PgxPool.Exec(ctx,
		`UPDATE transfers SET agent_profile_fk = $1 WHERE id = $2`, agentProfileID, id); err != nil {
		t.Fatalf("assigning agent: %v", err)
	}
}

func TestSenderVolumesWindows(t *testing.T) {
	ctx := context.Background()
	s := newStores(t)
	client := uuid.New()
	to := phone()

	// $100.00 an hour ago, $50.00 three days ago, $20.00 ten days ago and
	// $1000.00 forty days ago (outside every window, including the 30-day
	// average).
	recent := send(ctx, t, s, client, to, 10_000)
	backdate(ctx, t, s, recent.ID, time.Now().Add(-1*time.Hour))
	threeDays := send(ctx, t, s, client, to, 5_000)
	backdate(ctx, t, s, threeDays.ID, time.Now().AddDate(0, 0, -3))
	tenDays := send(ctx, t, s, client, to, 2_000)
	backdate(ctx, t, s, tenDays.ID, time.Now().AddDate(0, 0, -10))
	old := send(ctx, t, s, client, to, 100_000)
	backdate(ctx, t, s, old.ID, time.Now().AddDate(0, 0, -40))

	v, err := s.metrics.SenderVolumes(ctx, client, timezone)
	if err != nil {
		t.Fatalf("sender volumes: %v", err)
	}

	// Holds carry the 1.5% fee: 10150, 5075, 2030 cents.
	if v.Volume24hMinor != 10_150 {
		t.Errorf("24h volume = %d, want 10150", v.Volume24hMinor)
	}
	if v.Volume7dMinor != 15_225 {
		t.Errorf("7d volume = %d, want 15225", v.Volume7dMinor)
	}
	if v.Count7d != 2 {
		t.Errorf("7d count = %d, want 2", v.Count7d)
	}
	if v.Volume30dMinor != 17_255 {
		t.Errorf("30d volume = %d, want 17255", v.Volume30dMinor)
	}
	// An hour ago is outside the 1-hour window (the boundary is strict).
	if v.Count1h != 0 {
		t.Errorf("1h count = %d, want 0", v.Count1h)
	}
}

func TestSenderVolumesCountsTheCurrentHour(t *testing.T) {
	ctx := context.Background()
	s := newStores(t)
	client := uuid.New()
	to := phone()

	// Three transfers in the last few minutes: the velocity rule reads
	// this count, and the transfer being evaluated is one of them.
	for range 3 {
		send(ctx, t, s, client, to, 1_000)
	}

	v, err := s.metrics.SenderVolumes(ctx, client, timezone)
	if err != nil {
		t.Fatalf("sender volumes: %v", err)
	}
	if v.Count1h != 3 {
		t.Errorf("1h count = %d, want 3", v.Count1h)
	}
	if v.Count7d != 3 {
		t.Errorf("7d count = %d, want 3", v.Count7d)
	}
	if v.Volume24hMinor != 3*1_015 {
		t.Errorf("24h volume = %d, want %d", v.Volume24hMinor, 3*1_015)
	}
}

func TestSenderVolumesExcludeCancelled(t *testing.T) {
	ctx := context.Background()
	s := newStores(t)
	client := uuid.New()
	to := phone()

	kept := send(ctx, t, s, client, to, 1_000)
	dropped := send(ctx, t, s, client, to, 90_000)
	if _, err := s.transfers.CancelPreAccept(ctx, dropped.ID.String(), client, nil); err != nil {
		t.Fatalf("cancelling transfer: %v", err)
	}

	v, err := s.metrics.SenderVolumes(ctx, client, timezone)
	if err != nil {
		t.Fatalf("sender volumes: %v", err)
	}
	if v.Volume24hMinor != kept.HoldAmount {
		t.Errorf("24h volume = %d, want %d (cancelled transfer must not count)",
			v.Volume24hMinor, kept.HoldAmount)
	}
	if v.Count7d != 1 {
		t.Errorf("7d count = %d, want 1", v.Count7d)
	}
}

func TestSenderVolumesNighttimeIsLocalTime(t *testing.T) {
	ctx := context.Background()
	s := newStores(t)
	client := uuid.New()
	to := phone()

	kinshasa, err := time.LoadLocation(timezone)
	if err != nil {
		t.Fatalf("loading %s: %v", timezone, err)
	}
	day := time.Now().In(kinshasa).AddDate(0, 0, -2)
	at := func(hour int) time.Time {
		return time.Date(day.Year(), day.Month(), day.Day(), hour, 30, 0, 0, kinshasa)
	}

	// 23:30 and 03:30 local are inside the window; 12:30 and 22:30 are not
	// (22:30 local is 21:30 UTC — the case that fails if the count is read
	// in the server's zone).
	for _, hour := range []int{23, 3, 12, 22} {
		tr := send(ctx, t, s, client, to, 1_000)
		backdate(ctx, t, s, tr.ID, at(hour))
	}

	v, err := s.metrics.SenderVolumes(ctx, client, timezone)
	if err != nil {
		t.Fatalf("sender volumes: %v", err)
	}
	if v.Count7dNighttime != 2 {
		t.Errorf("7d night-time count = %d, want 2", v.Count7dNighttime)
	}
	if v.Count7d != 4 {
		t.Errorf("7d count = %d, want 4", v.Count7d)
	}
}

func TestAgentVolumes(t *testing.T) {
	ctx := context.Background()
	s := newStores(t)
	to := phone()

	profile, err := s.profiles.Add(ctx, agentprofile.NewAgentProfile{AccountID: uuid.New()})
	if err != nil {
		t.Fatalf("creating profile: %v", err)
	}

	// Two different senders served by the same agent, plus one transfer the
	// agent never took (it must not count against them).
	first := send(ctx, t, s, uuid.New(), to, 4_000)
	assign(ctx, t, s, first.ID, profile.ID)
	second := send(ctx, t, s, uuid.New(), to, 6_000)
	assign(ctx, t, s, second.ID, profile.ID)
	backdate(ctx, t, s, second.ID, time.Now().AddDate(0, 0, -3))
	send(ctx, t, s, uuid.New(), to, 50_000)

	v, err := s.metrics.AgentVolumes(ctx, profile.ID, timezone)
	if err != nil {
		t.Fatalf("agent volumes: %v", err)
	}
	if v.Volume24hMinor != first.HoldAmount {
		t.Errorf("24h volume = %d, want %d", v.Volume24hMinor, first.HoldAmount)
	}
	if v.Volume7dMinor != first.HoldAmount+second.HoldAmount {
		t.Errorf("7d volume = %d, want %d", v.Volume7dMinor, first.HoldAmount+second.HoldAmount)
	}
	if v.Count7d != 2 {
		t.Errorf("7d count = %d, want 2", v.Count7d)
	}
}

func TestRecipientDistinctSenders(t *testing.T) {
	ctx := context.Background()
	s := newStores(t)
	to := phone()

	// Two transfers from one client and one from another: two distinct
	// senders, not three transfers.
	first := uuid.New()
	send(ctx, t, s, first, to, 1_000)
	send(ctx, t, s, first, to, 2_000)
	send(ctx, t, s, uuid.New(), to, 3_000)

	// An eight-day-old transfer from a third client falls outside the week.
	stale := send(ctx, t, s, uuid.New(), to, 4_000)
	backdate(ctx, t, s, stale.ID, time.Now().AddDate(0, 0, -8))

	// Someone else's recipient is never counted here.
	send(ctx, t, s, uuid.New(), phone(), 5_000)

	count, err := s.metrics.RecipientDistinctSenders(ctx, to)
	if err != nil {
		t.Fatalf("distinct senders: %v", err)
	}
	if count != 2 {
		t.Errorf("distinct senders = %d, want 2", count)
	}
}

func TestVolumesOfUnknownSubjectAreZero(t *testing.T) {
	ctx := context.Background()
	s := newStores(t)

	// A first-ever sender has no history: the aggregation must answer zero,
	// not "no rows" — every rule still runs, on zeros.
	v, err := s.metrics.SenderVolumes(ctx, uuid.New(), timezone)
	if err != nil {
		t.Fatalf("sender volumes: %v", err)
	}
	if v != (compliance.Volumes{}) {
		t.Errorf("volumes = %+v, want the zero value", v)
	}

	count, err := s.metrics.RecipientDistinctSenders(ctx, phone())
	if err != nil {
		t.Fatalf("distinct senders: %v", err)
	}
	if count != 0 {
		t.Errorf("distinct senders = %d, want 0", count)
	}
}
