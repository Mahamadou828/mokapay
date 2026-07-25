package auth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"mokapay.app/app/services/user/internal/business/auth"
	"mokapay.app/core/cache"
	"mokapay.app/core/logger"
)

// Integration test: needs a running redis. Skipped unless
// TEST_REDIS_PORT is set:
//
//	TEST_REDIS_PORT=6379 go test ./internal/business/auth/usecase/auth/
func newTestLimiter(t *testing.T, cfg LimiterConfig) Limiter {
	t.Helper()

	port := os.Getenv("TEST_REDIS_PORT")
	if port == "" {
		t.Skip("TEST_REDIS_PORT not set; integration test needs redis")
	}

	c, err := cache.New(context.Background(), cache.Config{Host: "localhost", Port: port})
	if err != nil {
		t.Fatalf("connecting redis: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	return NewLimiter(logger.New(os.Stderr, logger.LevelInfo, "test", nil), c, cfg)
}

// uniquePhone keeps runs independent — redis state persists between them.
func uniquePhone() string {
	return fmt.Sprintf("+2439%09d", time.Now().UnixNano()%1_000_000_000)
}

func TestLimiterSendCooldownAndHourlyCap(t *testing.T) {
	l := newTestLimiter(t, LimiterConfig{
		SendCooldown: 300 * time.Millisecond,
		HourlyLimit:  3,
		VerifyLimit:  10,
		VerifyWindow: time.Minute,
	})
	ctx := context.Background()
	phone := uniquePhone()

	// First send passes; an immediate second hits the cooldown.
	if err := l.AllowSend(ctx, phone); err != nil {
		t.Fatalf("first send: %v", err)
	}
	if err := l.AllowSend(ctx, phone); !errors.Is(err, auth.ErrTooManyAttempts) {
		t.Fatalf("send inside cooldown = %v, want ErrTooManyAttempts", err)
	}

	// After the cooldown, sends pass until the hourly cap (3): sends 2
	// and 3 pass, send 4 is over budget even with the cooldown elapsed.
	for i := 2; i <= 3; i++ {
		time.Sleep(350 * time.Millisecond)
		if err := l.AllowSend(ctx, phone); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
	}
	time.Sleep(350 * time.Millisecond)
	if err := l.AllowSend(ctx, phone); !errors.Is(err, auth.ErrTooManyAttempts) {
		t.Fatalf("send over hourly cap = %v, want ErrTooManyAttempts", err)
	}

	// A different phone is unaffected.
	if err := l.AllowSend(ctx, uniquePhone()); err != nil {
		t.Fatalf("other phone: %v", err)
	}
}

func TestLimiterVerifyBudget(t *testing.T) {
	l := newTestLimiter(t, LimiterConfig{
		SendCooldown: time.Minute,
		HourlyLimit:  5,
		VerifyLimit:  4,
		VerifyWindow: time.Minute,
	})
	ctx := context.Background()
	phone := uniquePhone()

	for i := 1; i <= 4; i++ {
		if err := l.AllowVerify(ctx, phone); err != nil {
			t.Fatalf("verify %d: %v", i, err)
		}
	}
	if err := l.AllowVerify(ctx, phone); !errors.Is(err, auth.ErrTooManyAttempts) {
		t.Fatalf("verify over budget = %v, want ErrTooManyAttempts", err)
	}
}

func TestLimiterFailsOpenWithoutCache(t *testing.T) {
	// A nil cache (redis down at startup) disables limiting entirely —
	// auth must keep working.
	l := NewLimiter(logger.New(os.Stderr, logger.LevelInfo, "test", nil), nil, LimiterConfig{
		SendCooldown: time.Minute,
		HourlyLimit:  1,
		VerifyLimit:  1,
		VerifyWindow: time.Minute,
	})
	ctx := context.Background()

	for range 5 {
		if err := l.AllowSend(ctx, "+243900000000"); err != nil {
			t.Fatalf("nil-cache AllowSend: %v", err)
		}
		if err := l.AllowVerify(ctx, "+243900000000"); err != nil {
			t.Fatalf("nil-cache AllowVerify: %v", err)
		}
	}
}
