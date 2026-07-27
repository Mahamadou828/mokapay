// Package fxrate resolves the USD:CDF exchange rate: an HTTP provider
// behind a 5-minute Redis cache, with a static env-configured fallback so
// a rate-API hiccup never fails a quote. Rates travel as MICROS (rate ×
// 1e6, int64) — no floats past the provider edge.
//
// Lock semantics live upstream: GetQuote reads the cached rate
// statelessly; CreateTransfer reads it again and stamps it on the row —
// that stamp IS the lock.
package fxrate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"mokapay.app/core/cache"
	"mokapay.app/core/logger"
)

// Provider resolves the current CDF-per-USD rate in micros (2830.5 CDF
// per USD = 2_830_500_000).
type Provider interface {
	RateMicros(ctx context.Context) (int64, error)
}

// Static serves one configured rate — the local-dev default and the
// fallback of the HTTP provider.
type Static struct {
	Micros int64
}

func (s Static) RateMicros(context.Context) (int64, error) {
	if s.Micros <= 0 {
		return 0, errors.New("no static usd:cdf rate configured")
	}
	return s.Micros, nil
}

// HTTP pulls the rate from a keyless public endpoint (open.er-api.com
// shape: {"rates": {"CDF": 2830.5}}).
type HTTP struct {
	URL    string
	Client *http.Client
}

func NewHTTP(url string, timeout time.Duration) HTTP {
	return HTTP{URL: url, Client: &http.Client{Timeout: timeout}}
}

func (h HTTP) RateMicros(ctx context.Context) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.URL, nil)
	if err != nil {
		return 0, fmt.Errorf("building rate request: %w", err)
	}

	res, err := h.Client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("fetching rate: %w", err)
	}
	defer res.Body.Close() //nolint:errcheck // read-only body
	if res.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("rate provider returned %d", res.StatusCode)
	}

	var payload struct {
		Rates map[string]float64 `json:"rates"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		return 0, fmt.Errorf("decoding rate response: %w", err)
	}
	rate, ok := payload.Rates["CDF"]
	if !ok || rate <= 0 {
		return 0, errors.New("rate response carries no CDF quote")
	}

	return int64(math.Round(rate * 1_000_000)), nil
}

// Fallback serves the primary and degrades to the fallback on error —
// logged, never fatal (a rate hiccup must not fail a quote).
type Fallback struct {
	Primary Provider
	Backup  Provider
	Logger  *logger.Logger
}

func (f Fallback) RateMicros(ctx context.Context) (int64, error) {
	micros, err := f.Primary.RateMicros(ctx)
	if err == nil {
		return micros, nil
	}
	f.Logger.Error(ctx, "fxrate", "primary rate provider failed, serving fallback", "error", err)
	return f.Backup.RateMicros(ctx)
}

const cacheKey = "cache:fx:usd:cdf"

// Cached is the read-through decorator: most requests only ever see
// Redis; the TTL bounds display-vs-locked drift.
type Cached struct {
	Inner Provider
	Cache *cache.Cache
	TTL   time.Duration
}

func (c Cached) RateMicros(ctx context.Context) (int64, error) {
	if raw, err := c.Cache.Get(ctx, cacheKey); err == nil {
		if micros, perr := strconv.ParseInt(raw, 10, 64); perr == nil && micros > 0 {
			return micros, nil
		}
	} else if !errors.Is(err, cache.ErrNotFound) {
		// Redis down: fall through to the provider — degraded, not broken.
		_ = err
	}

	micros, err := c.Inner.RateMicros(ctx)
	if err != nil {
		return 0, err
	}
	// Best-effort: a failed cache write only costs the next request a
	// provider round-trip.
	_ = c.Cache.Set(ctx, cacheKey, strconv.FormatInt(micros, 10), c.TTL)

	return micros, nil
}

// Config assembles the chain from the service env.
type Config struct {
	ProviderURL string        // empty = static only (local dev)
	StaticRate  int64         // micros; the fallback and the no-URL default
	CacheTTL    time.Duration // the drift bound, default 5m
	HTTPTimeout time.Duration
}

// New builds the provider chain: [cache →] http-with-fallback | static.
// A nil cache skips the caching layer (tests).
func New(cfg Config, c *cache.Cache, log *logger.Logger) Provider {
	var p Provider = Static{Micros: cfg.StaticRate}
	if cfg.ProviderURL != "" {
		p = Fallback{
			Primary: NewHTTP(cfg.ProviderURL, cfg.HTTPTimeout),
			Backup:  Static{Micros: cfg.StaticRate},
			Logger:  log,
		}
	}
	if c != nil {
		p = Cached{Inner: p, Cache: c, TTL: cfg.CacheTTL}
	}
	return p
}
