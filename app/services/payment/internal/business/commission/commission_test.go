package commission_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"mokapay.app/app/services/payment/internal/business/commission"
	"mokapay.app/app/services/payment/internal/business/transfer"
)

// White-box-free unit tests: the whole commission math is pure integer
// arithmetic over values the transfer already stamped — no DB, no clock
// beyond the one passed in.

func TestSplitFeeSixtyThirtyTen(t *testing.T) {
	// The canonical case: a round fee splits exactly 60/30/10, and the FX
	// margin rides on top, untouched, for the platform.
	s := commission.SplitFee(1_000, 250)

	if s.Agent != 600 || s.Platform != 300 || s.GuaranteeFund != 100 {
		t.Fatalf("split of 1000 = %+v, want 600/300/100", s)
	}
	if s.Margin != 250 {
		t.Fatalf("margin = %d, want 250 (100%% platform, carried through)", s.Margin)
	}
}

func TestSplitFeeIsExhaustive(t *testing.T) {
	// Rounding must never lose (or invent) a cent: the guarantee fund
	// takes the remainder, so the three shares always re-add to the fee.
	// The DB CHECK enforces the same thing — this proves we never hit it.
	for fee := int64(0); fee <= 5_000; fee++ {
		s := commission.SplitFee(fee, 0)
		if got := s.Agent + s.Platform + s.GuaranteeFund; got != fee {
			t.Fatalf("fee %d: shares sum to %d (%+v)", fee, got, s)
		}
		if s.Agent < 0 || s.Platform < 0 || s.GuaranteeFund < 0 {
			t.Fatalf("fee %d: negative share %+v", fee, s)
		}
	}
}

func TestSplitFeeRounding(t *testing.T) {
	// 151 → agent 90.6 (half-up 91), platform 45.3 (half-up 45), the rest
	// (15) funds the guarantee pool.
	s := commission.SplitFee(151, 0)
	if s.Agent != 91 || s.Platform != 45 || s.GuaranteeFund != 15 {
		t.Fatalf("split of 151 = %+v, want 91/45/15", s)
	}
}

func TestPayableAmountUSDIsIdentity(t *testing.T) {
	// A USD fee is already in the guarantee balance's denomination.
	got, err := commission.PayableAmount(600, transfer.CurrencyUSD, 10_200, 10_200)
	if err != nil {
		t.Fatalf("PayableAmount: %v", err)
	}
	if got != 600 {
		t.Fatalf("payable = %d, want 600", got)
	}
}

func TestPayableAmountCDFConvertsAtTheTransfersOwnRate(t *testing.T) {
	// A CDF corridor: the client paid 287_245 francs and the transfer
	// reserved 10_149 cents for it, so the locked rate is 10_149/287_245
	// cents per franc. The agent's 2_547-franc share becomes
	// 2_547 × 10_149 / 287_245 = 89.99… → half-up 90 cents.
	got, err := commission.PayableAmount(2_547, transfer.CurrencyCDF, 287_245, 10_149)
	if err != nil {
		t.Fatalf("PayableAmount: %v", err)
	}
	if got != 90 {
		t.Fatalf("payable = %d, want 90 cents", got)
	}
}

func TestPayableAmountRejectsUnconvertible(t *testing.T) {
	// No stamped total means no rate to convert with — never guess one.
	if _, err := commission.PayableAmount(100, transfer.CurrencyCDF, 0, 10_000); err == nil {
		t.Fatal("PayableAmount with no total: want ErrNotPayable, got nil")
	}
	if _, err := commission.PayableAmount(100, "eur", 1_000, 1_000); err == nil {
		t.Fatal("PayableAmount with unknown currency: want ErrNotPayable, got nil")
	}
}

func TestAccountingWeekRunsTuesdayToMonday(t *testing.T) {
	// 2026-07-28 is a Tuesday: it opens its own week, and every day up to
	// the following Monday belongs to it.
	tuesday := time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)

	for offset := range 7 {
		day := tuesday.AddDate(0, 0, offset).Add(13 * time.Hour)
		if got := commission.AccountingWeek(day); !got.Equal(tuesday) {
			t.Fatalf("%s (%s) -> %s, want %s", day.Format(time.DateOnly), day.Weekday(), got, tuesday)
		}
	}

	// The next Tuesday opens the NEXT week.
	next := tuesday.AddDate(0, 0, 7)
	if got := commission.AccountingWeek(next); !got.Equal(next) {
		t.Fatalf("next Tuesday -> %s, want %s", got, next)
	}
	// And the Monday before belongs to the PREVIOUS one.
	if got := commission.AccountingWeek(tuesday.AddDate(0, 0, -1)); !got.Equal(tuesday.AddDate(0, 0, -7)) {
		t.Fatalf("the Monday before -> %s, want %s", got, tuesday.AddDate(0, 0, -7))
	}
}

func TestAccountingWeekIsUTC(t *testing.T) {
	// Late Monday in a +12 zone is already Tuesday UTC — the accounting
	// week is a calendar fact, not a local one.
	zone := time.FixedZone("UTC+12", 12*60*60)
	local := time.Date(2026, 7, 28, 6, 0, 0, 0, zone) // 2026-07-27 18:00 UTC (Monday)

	want := time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC)
	if got := commission.AccountingWeek(local); !got.Equal(want) {
		t.Fatalf("AccountingWeek(%s) = %s, want %s", local, got, want)
	}
}

func TestNewAccrualFromUSDTransfer(t *testing.T) {
	agent := uuid.New()
	at := time.Date(2026, 7, 30, 9, 0, 0, 0, time.UTC) // a Thursday

	// 100 USD, 2% fee, usd -> cdf with a 1_415-franc margin.
	nc, err := commission.NewAccrual(transfer.Transfer{
		ID:             uuid.New(),
		AgentProfileID: new(agent),
		FromCurrency:   transfer.CurrencyUSD,
		ToCurrency:     transfer.CurrencyCDF,
		FeeAmount:      200,
		MarginAmount:   1_415,
		TotalAmount:    10_200,
		HoldAmount:     10_200,
	}, at)
	if err != nil {
		t.Fatalf("NewAccrual: %v", err)
	}

	if nc.AgentAmount != 120 || nc.PlatformAmount != 60 || nc.GuaranteeFundAmount != 20 {
		t.Fatalf("split = %d/%d/%d, want 120/60/20", nc.AgentAmount, nc.PlatformAmount, nc.GuaranteeFundAmount)
	}
	// The margin stays in the to_currency, on its own — never folded into
	// the platform's fee share.
	if nc.MarginAmount != 1_415 || nc.MarginCurrency != transfer.CurrencyCDF {
		t.Fatalf("margin = %d %s, want 1415 cdf", nc.MarginAmount, nc.MarginCurrency)
	}
	if nc.FeeCurrency != transfer.CurrencyUSD || nc.PayableAmount != 120 {
		t.Fatalf("payable = %d %s, want 120 usd", nc.PayableAmount, nc.FeeCurrency)
	}
	if want := time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC); !nc.AccountingWeek.Equal(want) {
		t.Fatalf("accounting week = %s, want %s", nc.AccountingWeek, want)
	}
	if nc.AgentProfileID != agent {
		t.Fatalf("agent = %s, want %s", nc.AgentProfileID, agent)
	}
}

func TestNewAccrualNeedsAnAgent(t *testing.T) {
	// An unmatched transfer has nobody to pay — never accrue to a zero id.
	if _, err := commission.NewAccrual(transfer.Transfer{
		ID:           uuid.New(),
		FromCurrency: transfer.CurrencyUSD,
		ToCurrency:   transfer.CurrencyUSD,
		FeeAmount:    150,
		TotalAmount:  10_150,
		HoldAmount:   10_150,
	}, time.Now()); err == nil {
		t.Fatal("NewAccrual without an agent: want ErrNotPayable, got nil")
	}
}
