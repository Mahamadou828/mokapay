package repo_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"mokapay.app/app/services/user/internal/business/auth"
	"mokapay.app/app/services/user/internal/business/auth/repo"
	coredb "mokapay.app/core/db"
)

// Integration tests: need a migrated users database. Skipped unless
// TEST_DB_PORT is set, so `make user/test` stays green without infra:
//
//	TEST_DB_PORT=5433 go test ./internal/business/auth/repo/
func newTestStore(t *testing.T) repo.Store {
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

	return repo.New(db)
}

// testPhone returns a unique phone per call, keeping tests re-runnable
// without cleanup.
func testPhone() string {
	return fmt.Sprintf("+2439%09d", time.Now().UnixNano()%1_000_000_000)
}

func TestStoreOtpLifecycle(t *testing.T) {
	store := newTestStore(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	phone := testPhone()

	added, err := store.AddOtp(ctx, auth.NewOtp{
		PhoneNumber: phone,
		CodeHash:    "test-hash",
		Purpose:     auth.OtpPurposeLogin,
		ExpiresAt:   time.Now().Add(10 * time.Minute),
	})
	if err != nil {
		t.Fatalf("AddOtp: %v", err)
	}
	if added.ConsumedAt != nil {
		t.Fatal("fresh otp must not be consumed")
	}

	got, err := store.GetValidOtp(ctx, phone, auth.OtpPurposeLogin)
	if err != nil {
		t.Fatalf("GetValidOtp: %v", err)
	}
	if got.ID != added.ID {
		t.Fatalf("GetValidOtp returned %s, want %s", got.ID, added.ID)
	}

	if _, err := store.GetValidOtp(ctx, phone, auth.OtpPurposeReset); !errors.Is(err, auth.ErrOtpNotFound) {
		t.Fatalf("wrong purpose must be ErrOtpNotFound, got %v", err)
	}

	bumped, err := store.BumpOtpAttempts(ctx, added.ID.String())
	if err != nil {
		t.Fatalf("BumpOtpAttempts: %v", err)
	}
	if bumped.Attempts != added.Attempts+1 {
		t.Fatalf("attempts = %d, want %d", bumped.Attempts, added.Attempts+1)
	}

	// Resend: new hash + expiry, counted against attempts.
	refreshed, err := store.RefreshOtp(ctx, added.ID.String(), "resent-hash", time.Now().Add(10*time.Minute))
	if err != nil {
		t.Fatalf("RefreshOtp: %v", err)
	}
	if refreshed.CodeHash != "resent-hash" {
		t.Fatalf("code hash = %q, want %q", refreshed.CodeHash, "resent-hash")
	}
	if refreshed.Attempts != bumped.Attempts+1 {
		t.Fatalf("attempts after refresh = %d, want %d", refreshed.Attempts, bumped.Attempts+1)
	}

	consumed, err := store.ConsumeOtp(ctx, added.ID.String())
	if err != nil {
		t.Fatalf("ConsumeOtp: %v", err)
	}
	if consumed.ConsumedAt == nil {
		t.Fatal("consumed otp must carry ConsumedAt")
	}

	// Double-spend, refresh-after-consume and post-consume lookups must all miss.
	if _, err := store.ConsumeOtp(ctx, added.ID.String()); !errors.Is(err, auth.ErrOtpNotFound) {
		t.Fatalf("second consume must be ErrOtpNotFound, got %v", err)
	}
	if _, err := store.RefreshOtp(ctx, added.ID.String(), "late-hash", time.Now().Add(10*time.Minute)); !errors.Is(err, auth.ErrOtpNotFound) {
		t.Fatalf("refresh after consume must be ErrOtpNotFound, got %v", err)
	}
	if _, err := store.GetValidOtp(ctx, phone, auth.OtpPurposeLogin); !errors.Is(err, auth.ErrOtpNotFound) {
		t.Fatalf("consumed otp must not be valid, got %v", err)
	}
}

func TestStoreAccountLifecycle(t *testing.T) {
	store := newTestStore(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	phone := testPhone()
	email := "test@mokapay.app"
	sub := fmt.Sprintf("sub-%d", time.Now().UnixNano())

	added, err := store.AddAccount(ctx, auth.NewAccount{
		PhoneNumber:       phone,
		FirstName:         "Test",
		LastName:          "User",
		PreferredLanguage: auth.LanguageEn,
		Email:             &email,
	}, sub)
	if err != nil {
		t.Fatalf("AddAccount: %v", err)
	}
	if added.CognitoSub == nil || *added.CognitoSub != sub {
		t.Fatalf("cognito sub = %v, want %q", added.CognitoSub, sub)
	}
	if added.Role != auth.AccountRoleUser || added.Status != auth.AccountStatusActive {
		t.Fatalf("defaults = %s/%s, want user/active", added.Role, added.Status)
	}
	if added.Email == nil || *added.Email != email {
		t.Fatalf("email = %v, want %q", added.Email, email)
	}
	if added.LastLoginAt != nil {
		t.Fatal("fresh account must not carry LastLoginAt")
	}

	// Phone is unique among live rows.
	if _, err := store.AddAccount(ctx, auth.NewAccount{
		PhoneNumber: phone,
		FirstName:   "Dup",
		LastName:    "User",
	}, ""); !errors.Is(err, auth.ErrPhoneNumberTaken) {
		t.Fatalf("duplicate phone must be ErrPhoneNumberTaken, got %v", err)
	}

	// So is the Cognito sub — but only for provisioned rows: an account
	// without a sub (stored NULL) must not conflict with anything.
	if _, err := store.AddAccount(ctx, auth.NewAccount{
		PhoneNumber: testPhone(),
		FirstName:   "Dup",
		LastName:    "Sub",
	}, sub); !errors.Is(err, auth.ErrCognitoSubTaken) {
		t.Fatalf("duplicate sub must be ErrCognitoSubTaken, got %v", err)
	}
	unprovisioned, err := store.AddAccount(ctx, auth.NewAccount{
		PhoneNumber: testPhone(),
		FirstName:   "No",
		LastName:    "Sub",
	}, "")
	if err != nil {
		t.Fatalf("AddAccount without sub: %v", err)
	}
	if unprovisioned.CognitoSub != nil {
		t.Fatalf("unprovisioned sub = %v, want nil", unprovisioned.CognitoSub)
	}

	bySub, err := store.GetAccountBySub(ctx, sub)
	if err != nil {
		t.Fatalf("GetAccountBySub: %v", err)
	}
	if bySub.ID != added.ID {
		t.Fatalf("GetAccountBySub returned %s, want %s", bySub.ID, added.ID)
	}
	if _, err := store.GetAccountBySub(ctx, "sub-unknown"); !errors.Is(err, auth.ErrAccountNotFound) {
		t.Fatalf("unknown sub must be ErrAccountNotFound, got %v", err)
	}

	byPhone, err := store.GetAccountByPhone(ctx, phone)
	if err != nil {
		t.Fatalf("GetAccountByPhone: %v", err)
	}
	if byPhone.ID != added.ID {
		t.Fatalf("GetAccountByPhone returned %s, want %s", byPhone.ID, added.ID)
	}

	byID, err := store.GetAccount(ctx, added.ID.String())
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}
	if byID.PhoneNumber != phone {
		t.Fatalf("GetAccount phone = %q, want %q", byID.PhoneNumber, phone)
	}

	loginAt := time.Now()
	stamped, err := store.SetLastLogin(ctx, added.ID.String(), loginAt)
	if err != nil {
		t.Fatalf("SetLastLogin: %v", err)
	}
	if stamped.LastLoginAt == nil {
		t.Fatal("SetLastLogin must set LastLoginAt")
	}

	if _, err := store.GetAccountByPhone(ctx, testPhone()); !errors.Is(err, auth.ErrAccountNotFound) {
		t.Fatalf("unknown phone must be ErrAccountNotFound, got %v", err)
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
