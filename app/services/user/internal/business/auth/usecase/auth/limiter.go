package auth

import (
	"context"
	"fmt"
	"time"

	"mokapay.app/app/services/user/internal/business/auth"
	"mokapay.app/core/cache"
	"mokapay.app/core/logger"
)

// Limiter enforces the per-phone OTP budgets on redis, keyed on the
// E.164 phone — the phone is the resource being consumed (each send is
// an SMS with a cost; attackers rotate IPs, not victims' numbers).
//
// Deliberately NOT a token bucket: a SetNX cooldown (a 1-token bucket
// refilling every window) plus a fixed-window counter compose into the
// same practical guarantee with primitives core/cache already has. The
// fixed window's boundary burst is bounded by the cooldown anyway.
//
// FAIL-OPEN by design: a nil cache or a redis outage logs and allows —
// rate limiting protects SMS cost, the DB attempt cap keeps holding the
// security floor, and a cache outage must never lock logins out.
type Limiter struct {
	cache        *cache.Cache // nil = limiting disabled
	logger       *logger.Logger
	sendCooldown time.Duration
	hourlyLimit  int
	verifyLimit  int
	verifyWindow time.Duration
}

// LimiterConfig carries the env-resolved knobs.
type LimiterConfig struct {
	SendCooldown time.Duration // min gap between two sends to one phone
	HourlyLimit  int           // sends per phone per hour
	VerifyLimit  int           // verify attempts per phone per window (cross-code)
	VerifyWindow time.Duration
}

// NewLimiter builds the limiter; a nil cache disables it (local dev
// without redis).
func NewLimiter(l *logger.Logger, c *cache.Cache, cfg LimiterConfig) Limiter {
	return Limiter{
		cache:        c,
		logger:       l,
		sendCooldown: cfg.SendCooldown,
		hourlyLimit:  cfg.HourlyLimit,
		verifyLimit:  cfg.VerifyLimit,
		verifyWindow: cfg.VerifyWindow,
	}
}

// AllowSend gates an OTP send: cooldown first (the cheap rejection),
// then the hourly cap.
func (l Limiter) AllowSend(ctx context.Context, phone string) error {
	if l.cache == nil {
		return nil
	}

	ok, err := l.cache.SetNX(ctx, "coord:otp:cd:"+phone, "1", l.sendCooldown)
	if err != nil {
		l.logger.Error(ctx, "Auth.Limiter: cooldown check failed — failing open", "err", err)
		return nil
	}
	if !ok {
		return fmt.Errorf("otp send cooldown active: %w", auth.ErrTooManyAttempts)
	}

	n, err := l.cache.Incr(ctx, "coord:otp:hr:"+phone, time.Hour)
	if err != nil {
		l.logger.Error(ctx, "Auth.Limiter: hourly counter failed — failing open", "err", err)
		return nil
	}
	if n > int64(l.hourlyLimit) {
		return fmt.Errorf("otp hourly limit reached: %w", auth.ErrTooManyAttempts)
	}

	return nil
}

// AllowVerify gates a code check. The DB caps attempts per CODE; this
// caps guesses per PHONE across codes, so cycling fresh codes doesn't
// reset the budget.
func (l Limiter) AllowVerify(ctx context.Context, phone string) error {
	if l.cache == nil {
		return nil
	}

	n, err := l.cache.Incr(ctx, "coord:otp:vf:"+phone, l.verifyWindow)
	if err != nil {
		l.logger.Error(ctx, "Auth.Limiter: verify counter failed — failing open", "err", err)
		return nil
	}
	if n > int64(l.verifyLimit) {
		return fmt.Errorf("otp verify limit reached: %w", auth.ErrTooManyAttempts)
	}

	return nil
}
