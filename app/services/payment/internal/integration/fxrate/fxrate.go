// Package fxrate resolves the CDF exchange rate of every currency the
// send ladder sells: an HTTP provider behind a 5-minute Redis cache, with
// static env-configured fallbacks so a rate-API hiccup never fails a
// quote. Rates travel as MICROS (rate × 1e6, int64) — no floats past the
// provider edge.
//
// One rate per SOURCE currency, always quoted against CDF. Each currency
// gets its own upstream call (base = that currency, read Rates["CDF"])
// and its own cache key, which keeps the chain the exact shape it had
// when USD was the only base — no inversion step, no shared payload
// whose staleness would span six corridors at once.
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
	"strings"
	"time"

	"mokapay.app/app/services/payment/internal/business/transfer"
	"mokapay.app/core/cache"
	"mokapay.app/core/logger"
)

// Provider resolves the current CDF-per-unit rate of a source currency,
// in micros (2830.5 CDF per USD = 2_830_500_000).
type Provider interface {
	RateMicros(ctx context.Context, from transfer.Currency) (int64, error)
}

// Static serves configured rates — the local-dev default and the fallback
// of the HTTP provider. A currency with no entry has no rate: an error,
// never another currency's number served in its place.
type Static struct {
	Micros map[transfer.Currency]int64
}

func (s Static) RateMicros(_ context.Context, from transfer.Currency) (int64, error) {
	micros, ok := s.Micros[from]
	if !ok || micros <= 0 {
		return 0, fmt.Errorf("no static %s:cdf rate configured", from)
	}
	return micros, nil
}

// DefaultStaticRates are the launch fallbacks, CDF per source unit in
// micros — indicative mid-market figures, overridden one by one through
// FX_STATIC_RATE_<CCY>_MICROS and refreshed on every read wherever an
// HTTP provider is configured.
func DefaultStaticRates() map[transfer.Currency]int64 {
	return map[transfer.Currency]int64{
		transfer.CurrencyEUR: 2_885_000_000,
		transfer.CurrencyUSD: 2_830_000_000,
		transfer.CurrencyZAR: 154_000_000,
		transfer.CurrencyAOA: 3_080_000,
		transfer.CurrencyZMW: 108_000_000,
		transfer.CurrencyTZS: 1_090_000,
	}
}

// HTTP pulls the rate from a keyless public endpoint (open.er-api.com
// shape: GET <BaseURL>/EUR → {"rates": {"CDF": 2885.1}}). The source
// currency is the request's base, so one currency costs one call.
type HTTP struct {
	BaseURL string
	Client  *http.Client
}

func NewHTTP(baseURL string, timeout time.Duration) HTTP {
	return HTTP{
		BaseURL: strings.TrimSuffix(baseURL, "/"),
		Client:  &http.Client{Timeout: timeout},
	}
}

func (h HTTP) RateMicros(ctx context.Context, from transfer.Currency) (int64, error) {
	url := h.BaseURL + "/" + strings.ToUpper(string(from))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, fmt.Errorf("building %s rate request: %w", from, err)
	}

	res, err := h.Client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("fetching %s rate: %w", from, err)
	}
	defer res.Body.Close() //nolint:errcheck // read-only body
	if res.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("rate provider returned %d for %s", res.StatusCode, from)
	}

	var payload struct {
		Rates map[string]float64 `json:"rates"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		return 0, fmt.Errorf("decoding %s rate response: %w", from, err)
	}
	rate, ok := payload.Rates["CDF"]
	if !ok || rate <= 0 {
		return 0, fmt.Errorf("%s rate response carries no CDF quote", from)
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

func (f Fallback) RateMicros(ctx context.Context, from transfer.Currency) (int64, error) {
	micros, err := f.Primary.RateMicros(ctx, from)
	if err == nil {
		return micros, nil
	}
	f.Logger.Error(ctx, "fxrate", "primary rate provider failed, serving fallback",
		"currency", string(from), "error", err)
	return f.Backup.RateMicros(ctx, from)
}

// cacheKey is per source currency: one corridor going stale never drags
// the other five with it.
func cacheKey(from transfer.Currency) string {
	return "cache:fx:" + string(from) + ":cdf"
}

// Cached is the read-through decorator: most requests only ever see
// Redis; the TTL bounds display-vs-locked drift.
type Cached struct {
	Inner Provider
	Cache *cache.Cache
	TTL   time.Duration
}

func (c Cached) RateMicros(ctx context.Context, from transfer.Currency) (int64, error) {
	key := cacheKey(from)
	if raw, err := c.Cache.Get(ctx, key); err == nil {
		if micros, perr := strconv.ParseInt(raw, 10, 64); perr == nil && micros > 0 {
			return micros, nil
		}
	} else if !errors.Is(err, cache.ErrNotFound) {
		// Redis down: fall through to the provider — degraded, not broken.
		_ = err
	}

	micros, err := c.Inner.RateMicros(ctx, from)
	if err != nil {
		return 0, err
	}
	// Best-effort: a failed cache write only costs the next request a
	// provider round-trip.
	_ = c.Cache.Set(ctx, key, strconv.FormatInt(micros, 10), c.TTL)

	return micros, nil
}

// Config assembles the chain from the service env.
type Config struct {
	ProviderURL string                      // endpoint PREFIX (the base currency is appended); empty = static only (local dev)
	StaticRates map[transfer.Currency]int64 // micros per source currency; the fallback and the no-URL default
	CacheTTL    time.Duration               // the drift bound, default 5m
	HTTPTimeout time.Duration
}

// New builds the provider chain: [cache →] http-with-fallback | static.
// A nil cache skips the caching layer (tests).
func New(cfg Config, c *cache.Cache, log *logger.Logger) Provider {
	var p Provider = Static{Micros: cfg.StaticRates}
	if cfg.ProviderURL != "" {
		p = Fallback{
			Primary: NewHTTP(cfg.ProviderURL, cfg.HTTPTimeout),
			Backup:  Static{Micros: cfg.StaticRates},
			Logger:  log,
		}
	}
	if c != nil {
		p = Cached{Inner: p, Cache: c, TTL: cfg.CacheTTL}
	}
	return p
}
