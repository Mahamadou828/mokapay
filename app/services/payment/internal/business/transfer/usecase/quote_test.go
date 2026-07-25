package usecase

import (
	"context"
	"errors"
	"io"
	"testing"

	"mokapay.app/app/services/payment/internal/business/transfer"
	"mokapay.app/app/services/payment/internal/integration/fxrate"
	"mokapay.app/core/logger"
)

// White-box unit tests: the quote math is pure integer arithmetic over
// the injected rate — no DB, no network.
func quoter(rateMicros int64) UseCase {
	return UseCase{
		logger: logger.New(io.Discard, logger.LevelInfo, "test", nil),
		fx:     fxrate.Static{Micros: rateMicros},
		cfg: Config{
			FeeSameBps: 150,
			FeeFXBps:   200,
			MarginBps:  50,
			MaxAmount:  1_000_000_000,
		},
	}
}

func TestQuoteUSDToUSD(t *testing.T) {
	// Same currency: 1.5% fee, no rate involved at all (rate 0 would fail).
	q, err := quoter(0).quote(context.Background(), transfer.CurrencyUSD, 10_000, transfer.CurrencyUSD)
	if err != nil {
		t.Fatalf("quote: %v", err)
	}
	if q.FeeAmount != 150 || q.TotalAmount != 10_150 || q.ToAmount != 10_000 ||
		q.HoldAmount != 10_150 || q.RateMicros != nil || q.MarginAmount != 0 {
		t.Fatalf("usd->usd quote = %+v", q)
	}
}

func TestQuoteUSDToCDF(t *testing.T) {
	// 100 USD at 2830.5: gross = 10000 × 2_830_500_000 / 1e8 = 283050
	// francs; margin 0.5% half-up = 1415; to = 281635. Fee 2% = 200
	// cents; hold = total = 10200 cents.
	q, err := quoter(2_830_500_000).quote(context.Background(), transfer.CurrencyUSD, 10_000, transfer.CurrencyCDF)
	if err != nil {
		t.Fatalf("quote: %v", err)
	}
	if q.FeeAmount != 200 || q.TotalAmount != 10_200 || q.HoldAmount != 10_200 {
		t.Fatalf("usd->cdf fee/total/hold = %+v", q)
	}
	if q.MarginAmount != 1_415 || q.ToAmount != 281_635 {
		t.Fatalf("usd->cdf margin/to = %+v", q)
	}
	if q.RateMicros == nil || *q.RateMicros != 2_830_500_000 {
		t.Fatalf("usd->cdf rate = %+v", q.RateMicros)
	}
}

func TestQuoteCDFToCDF(t *testing.T) {
	// 283000 francs, fee 1.5% = 4245, total 287245. Hold converts at
	// 2830.5: 287245 × 1e8 / 2_830_500_000 = 10148.2… → ceil 10149 cents.
	// No client-facing rate.
	q, err := quoter(2_830_500_000).quote(context.Background(), transfer.CurrencyCDF, 283_000, transfer.CurrencyCDF)
	if err != nil {
		t.Fatalf("quote: %v", err)
	}
	if q.FeeAmount != 4_245 || q.TotalAmount != 287_245 || q.ToAmount != 283_000 {
		t.Fatalf("cdf->cdf quote = %+v", q)
	}
	if q.HoldAmount != 10_149 {
		t.Fatalf("cdf->cdf hold = %d, want 10149 (ceil)", q.HoldAmount)
	}
	if q.RateMicros != nil || q.MarginAmount != 0 {
		t.Fatalf("cdf->cdf must carry no client-facing rate/margin: %+v", q)
	}
}

func TestQuoteGuards(t *testing.T) {
	u := quoter(2_830_000_000)
	ctx := context.Background()

	if _, err := u.quote(ctx, transfer.CurrencyCDF, 1000, transfer.CurrencyUSD); !errors.Is(err, transfer.ErrCorridorNotAllowed) {
		t.Errorf("cdf->usd = %v, want ErrCorridorNotAllowed", err)
	}
	if _, err := u.quote(ctx, "eur", 1000, transfer.CurrencyCDF); !errors.Is(err, transfer.ErrCorridorNotAllowed) {
		t.Errorf("eur source = %v, want ErrCorridorNotAllowed", err)
	}
	if _, err := u.quote(ctx, transfer.CurrencyUSD, 0, transfer.CurrencyUSD); !errors.Is(err, transfer.ErrInvalidAmount) {
		t.Errorf("zero amount = %v, want ErrInvalidAmount", err)
	}
	if _, err := u.quote(ctx, transfer.CurrencyUSD, 1_000_000_001, transfer.CurrencyUSD); !errors.Is(err, transfer.ErrInvalidAmount) {
		t.Errorf("over cap = %v, want ErrInvalidAmount", err)
	}
	// A dead rate provider fails cross-currency quotes cleanly.
	if _, err := quoter(0).quote(ctx, transfer.CurrencyUSD, 1000, transfer.CurrencyCDF); err == nil {
		t.Error("usd->cdf with no rate must fail")
	}
}
