package repo_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"mokapay.app/app/services/user/internal/business/auth"
	authrepo "mokapay.app/app/services/user/internal/business/auth/repo"
	"mokapay.app/app/services/user/internal/business/notification"
	"mokapay.app/app/services/user/internal/business/notification/repo"
	coredb "mokapay.app/core/db"
)

// Integration tests: need a migrated users database (0002). Skipped unless
// TEST_DB_PORT is set, so `make user/test` stays green without infra:
//
//	TEST_DB_PORT=5433 go test ./internal/business/notification/repo/
func newTestStores(t *testing.T) (repo.Store, authrepo.Store) {
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

	return repo.New(db), authrepo.New(db)
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// testAccount creates the FK target every notification row needs.
func testAccount(ctx context.Context, t *testing.T, accounts authrepo.Store) uuid.UUID {
	t.Helper()

	phone := fmt.Sprintf("+2439%09d", time.Now().UnixNano()%1_000_000_000)
	acc, err := accounts.AddAccount(ctx, auth.NewAccount{
		PhoneNumber: phone,
		FirstName:   "Notif",
		LastName:    "Tester",
	}, "")
	if err != nil {
		t.Fatalf("creating test account: %v", err)
	}
	return acc.ID
}

func TestNotificationLifecycle(t *testing.T) {
	store, accounts := newTestStores(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	accountID := testAccount(ctx, t, accounts)

	// Add: with and without data payload.
	data := `{"transfer_id":"7f4d2a"}`
	first, err := store.AddNotification(ctx, notification.NewNotification{
		AccountID: accountID,
		EventType: "transfer.offered",
		Title:     "New transfer",
		Body:      "You have a transfer to review",
		Data:      new(data),
	})
	if err != nil {
		t.Fatalf("AddNotification: %v", err)
	}
	if first.Status != notification.StatusUnread {
		t.Errorf("new notification status = %q, want unread", first.Status)
	}
	// JSONB canonicalizes formatting — compare parsed values, not bytes.
	if first.Data == nil {
		t.Fatalf("data came back nil")
	}
	var got, want map[string]any
	if err := json.Unmarshal([]byte(*first.Data), &got); err != nil {
		t.Fatalf("stored data is not JSON: %v", err)
	}
	if err := json.Unmarshal([]byte(data), &want); err != nil {
		t.Fatalf("test data is not JSON: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("data round-trip = %q, want %q", *first.Data, data)
	}
	if first.ReadAt != nil {
		t.Errorf("new notification has ReadAt set")
	}

	second, err := store.AddNotification(ctx, notification.NewNotification{
		AccountID: accountID,
		EventType: "file.rejected",
		Title:     "Document rejected",
		Body:      "Your proof was rejected",
	})
	if err != nil {
		t.Fatalf("AddNotification (no data): %v", err)
	}
	if second.Data != nil {
		t.Errorf("nil data came back as %q", *second.Data)
	}

	// List: full inbox, newest first.
	all, err := store.ListNotifications(ctx, accountID.String(), notification.Filter{}, 50)
	if err != nil {
		t.Fatalf("ListNotifications: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("inbox size = %d, want 2", len(all))
	}
	if all[0].ID != second.ID {
		t.Errorf("inbox not newest-first")
	}

	// MarkRead: flips + stamps, and is idempotent (read_at preserved).
	read, err := store.MarkRead(ctx, first.ID.String(), accountID.String())
	if err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
	if read.Status != notification.StatusRead || read.ReadAt == nil {
		t.Fatalf("MarkRead result: status=%q readAt=%v", read.Status, read.ReadAt)
	}
	again, err := store.MarkRead(ctx, first.ID.String(), accountID.String())
	if err != nil {
		t.Fatalf("MarkRead (again): %v", err)
	}
	if !again.ReadAt.Equal(*read.ReadAt) {
		t.Errorf("re-read changed read_at: %v → %v", read.ReadAt, again.ReadAt)
	}

	// Ownership guard: another account can't mark it.
	otherID := testAccount(ctx, t, accounts)
	if _, err := store.MarkRead(ctx, second.ID.String(), otherID.String()); !errors.Is(err, notification.ErrNotificationNotFound) {
		t.Errorf("foreign MarkRead error = %v, want ErrNotificationNotFound", err)
	}

	// Status filter: one unread left.
	unreadStatus := notification.StatusUnread
	unread, err := store.ListNotifications(ctx, accountID.String(), notification.Filter{Status: new(unreadStatus)}, 50)
	if err != nil {
		t.Fatalf("ListNotifications(unread): %v", err)
	}
	if len(unread) != 1 || unread[0].ID != second.ID {
		t.Fatalf("unread filter returned %d rows", len(unread))
	}

	// MarkAllRead: flips the remaining one, then finds nothing to do.
	flipped, err := store.MarkAllRead(ctx, accountID.String())
	if err != nil {
		t.Fatalf("MarkAllRead: %v", err)
	}
	if flipped != 1 {
		t.Errorf("MarkAllRead flipped %d, want 1", flipped)
	}
	flipped, err = store.MarkAllRead(ctx, accountID.String())
	if err != nil {
		t.Fatalf("MarkAllRead (empty): %v", err)
	}
	if flipped != 0 {
		t.Errorf("second MarkAllRead flipped %d, want 0", flipped)
	}
}

func TestWebpushSubscriptionLifecycle(t *testing.T) {
	store, accounts := newTestStores(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	accountID := testAccount(ctx, t, accounts)
	endpoint := fmt.Sprintf("https://push.example.com/reg/%s", uuid.NewString())

	ua := "Mozilla/5.0 (test)"
	added, err := store.AddSubscription(ctx, notification.NewPushSubscription{
		AccountID: accountID,
		Endpoint:  endpoint,
		P256dh:    "p256dh-key",
		Auth:      "auth-secret",
		UserAgent: new(ua),
	})
	if err != nil {
		t.Fatalf("AddSubscription: %v", err)
	}
	if added.UserAgent == nil || *added.UserAgent != ua {
		t.Errorf("user agent round-trip = %v", added.UserAgent)
	}

	// Upsert: same endpoint, rotated keys, new owner — same row re-bound.
	otherID := testAccount(ctx, t, accounts)
	rebound, err := store.AddSubscription(ctx, notification.NewPushSubscription{
		AccountID: otherID,
		Endpoint:  endpoint,
		P256dh:    "p256dh-key-2",
		Auth:      "auth-secret-2",
	})
	if err != nil {
		t.Fatalf("AddSubscription (upsert): %v", err)
	}
	if rebound.ID != added.ID {
		t.Errorf("upsert created a new row: %s → %s", added.ID, rebound.ID)
	}
	if rebound.AccountID != otherID || rebound.P256dh != "p256dh-key-2" {
		t.Errorf("upsert did not rebind/rotate: %+v", rebound)
	}
	if rebound.UserAgent == nil || *rebound.UserAgent != ua {
		t.Errorf("upsert with nil user agent should keep the stored one")
	}

	// The device now belongs to otherID; the first account has no routes.
	routes, err := store.ListSubscriptions(ctx, accountID.String())
	if err != nil {
		t.Fatalf("ListSubscriptions: %v", err)
	}
	if len(routes) != 0 {
		t.Errorf("old owner still has %d routes", len(routes))
	}
	routes, err = store.ListSubscriptions(ctx, otherID.String())
	if err != nil {
		t.Fatalf("ListSubscriptions (new owner): %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("new owner has %d routes, want 1", len(routes))
	}

	// Delete is ownership-guarded and idempotent.
	if err := store.DeleteSubscription(ctx, accountID.String(), endpoint); err != nil {
		t.Fatalf("DeleteSubscription (foreign, no-op): %v", err)
	}
	if routes, _ = store.ListSubscriptions(ctx, otherID.String()); len(routes) != 1 {
		t.Fatalf("foreign delete removed the route")
	}
	if err := store.DeleteSubscription(ctx, otherID.String(), endpoint); err != nil {
		t.Fatalf("DeleteSubscription: %v", err)
	}
	if routes, _ = store.ListSubscriptions(ctx, otherID.String()); len(routes) != 0 {
		t.Fatalf("delete left %d routes", len(routes))
	}

	// Re-subscribing after delete inserts a fresh live row (soft-deleted
	// rows never conflict).
	fresh, err := store.AddSubscription(ctx, notification.NewPushSubscription{
		AccountID: otherID,
		Endpoint:  endpoint,
		P256dh:    "p256dh-key-3",
		Auth:      "auth-secret-3",
	})
	if err != nil {
		t.Fatalf("AddSubscription (after delete): %v", err)
	}
	if fresh.ID == added.ID {
		t.Errorf("resurrected the soft-deleted row instead of inserting")
	}
}
