package usecase_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"mokapay.app/app/services/user/internal/business/auth"
	authrepo "mokapay.app/app/services/user/internal/business/auth/repo"
	"mokapay.app/app/services/user/internal/business/notification"
	"mokapay.app/app/services/user/internal/business/notification/usecase"
	coredb "mokapay.app/core/db"
	"mokapay.app/core/logger"
)

// fakeSender records deliveries and simulates a dead route: sends to
// goneEndpoint return ErrSubscriptionGone, failEndpoint errors plainly.
type fakeSender struct {
	sent         []string // endpoints delivered to, in order
	payloads     [][]byte
	goneEndpoint string
	failEndpoint string
}

func (f *fakeSender) Send(_ context.Context, sub notification.PushSubscription, payload []byte) error {
	switch sub.Endpoint {
	case f.goneEndpoint:
		return notification.ErrSubscriptionGone
	case f.failEndpoint:
		return errors.New("push service on fire")
	}
	f.sent = append(f.sent, sub.Endpoint)
	f.payloads = append(f.payloads, payload)
	return nil
}

// Integration test: needs a migrated users database. Skipped unless
// TEST_DB_PORT is set:
//
//	TEST_DB_PORT=5433 go test ./internal/business/notification/usecase/
func newTestUseCase(t *testing.T) (usecase.UseCase, *fakeSender, uuid.UUID) {
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

	phone := fmt.Sprintf("+2439%09d", time.Now().UnixNano()%1_000_000_000)
	acc, err := authrepo.New(db).AddAccount(context.Background(), auth.NewAccount{
		PhoneNumber: phone,
		FirstName:   "Push",
		LastName:    "Tester",
	}, "")
	if err != nil {
		t.Fatalf("creating test account: %v", err)
	}

	sender := &fakeSender{}
	log := logger.New(os.Stderr, logger.LevelInfo, "test", nil)
	return usecase.New(log, db, sender, "test-vapid-pub"), sender, acc.ID
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func TestPushDeliversAndPrunes(t *testing.T) {
	uc, sender, accountID := newTestUseCase(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Three routes: one healthy, one gone (must be pruned), one failing
	// (must be kept and not fail the call).
	healthy := "https://push.example.com/ok/" + uuid.NewString()
	gone := "https://push.example.com/gone/" + uuid.NewString()
	failing := "https://push.example.com/fail/" + uuid.NewString()
	sender.goneEndpoint, sender.failEndpoint = gone, failing

	for _, ep := range []string{healthy, gone, failing} {
		if _, err := uc.Subscribe(ctx, notification.NewPushSubscription{
			AccountID: accountID,
			Endpoint:  ep,
			P256dh:    "k",
			Auth:      "a",
		}); err != nil {
			t.Fatalf("Subscribe(%s): %v", ep, err)
		}
	}

	data := `{"transfer_id":"abc"}`
	created, err := uc.Push(ctx, notification.NewNotification{
		AccountID: accountID,
		EventType: "transfer.offered",
		Title:     "New transfer",
		Body:      "Check it",
		Data:      new(data),
	})
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if created.Status != notification.StatusUnread {
		t.Errorf("pushed notification status = %q", created.Status)
	}

	// Delivered to the healthy route only, with the full payload.
	if len(sender.sent) != 1 || sender.sent[0] != healthy {
		t.Fatalf("delivered to %v, want only %s", sender.sent, healthy)
	}
	var payload struct {
		Title     string          `json:"title"`
		Body      string          `json:"body"`
		EventType string          `json:"event_type"`
		Data      json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(sender.payloads[0], &payload); err != nil {
		t.Fatalf("payload is not JSON: %v", err)
	}
	if payload.Title != "New transfer" || payload.EventType != "transfer.offered" || len(payload.Data) == 0 {
		t.Errorf("payload = %s", sender.payloads[0])
	}

	// The inbox row exists regardless of delivery.
	inbox, err := uc.List(ctx, accountID, notification.Filter{}, 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(inbox) != 1 || inbox[0].ID != created.ID {
		t.Fatalf("inbox = %d rows", len(inbox))
	}

	// Second push: the gone route was pruned, the failing one is retried.
	sender.sent = nil
	if _, err := uc.Push(ctx, notification.NewNotification{
		AccountID: accountID,
		EventType: "transfer.funded",
		Title:     "Funded",
		Body:      "Money moved",
	}); err != nil {
		t.Fatalf("Push (second): %v", err)
	}
	if len(sender.sent) != 1 || sender.sent[0] != healthy {
		t.Fatalf("second push delivered to %v", sender.sent)
	}

	subs := listEndpoints(ctx, t, uc, accountID)
	if len(subs) != 2 {
		t.Fatalf("routes after prune = %v, want healthy+failing", subs)
	}
	for _, ep := range subs {
		if ep == gone {
			t.Errorf("gone endpoint survived pruning")
		}
	}
}

// listEndpoints peeks at the stored routes directly — the usecase exposes
// no subscription listing (by design).
func listEndpoints(ctx context.Context, t *testing.T, _ usecase.UseCase, accountID uuid.UUID) []string {
	t.Helper()

	db := coredb.New(coredb.PostgresConfig{
		Host:     getenv("TEST_DB_HOST", "localhost"),
		Port:     os.Getenv("TEST_DB_PORT"),
		Username: getenv("TEST_DB_USER", "mokapay"),
		Password: getenv("TEST_DB_PASSWORD", "mokapay"),
		Database: getenv("TEST_DB_NAME", "users"),
	})
	if err := db.Open(); err != nil {
		t.Fatalf("opening database: %v", err)
	}
	defer db.Close()

	rows, err := db.PgxPool.Query(ctx,
		"SELECT endpoint FROM users_ext.webpush_subscriptions WHERE account_fk = $1", accountID.String())
	if err != nil {
		t.Fatalf("listing endpoints: %v", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var ep string
		if err := rows.Scan(&ep); err != nil {
			t.Fatalf("scanning endpoint: %v", err)
		}
		out = append(out, ep)
	}
	return out
}
