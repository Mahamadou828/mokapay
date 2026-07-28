package compliance_test

import (
	"testing"

	"github.com/google/uuid"
	"mokapay.app/app/services/payment/internal/business/compliance"
)

// The unit conversion is the whole point of this type: payment counts
// cents, the compliance contract is whole dollars, and a factor of 100 in
// the wrong place makes every configured threshold mean something else.
func TestNewAccountMetricsConvertsToWholeUSD(t *testing.T) {
	account := uuid.New()

	m := compliance.NewAccountMetrics(account, 10_150, compliance.Volumes{
		Volume24hMinor:   1_000_000, // $10,000.00
		Volume7dMinor:    2_550_075, // $25,500.75
		Count7d:          9,
		Volume30dMinor:   30_000_000, // $300,000.00 over 30 days = $10,000/day
		Count1h:          2,
		Count7dNighttime: 4,
	}, 12)

	if m.AccountID != account {
		t.Errorf("account id = %s, want %s", m.AccountID, account)
	}
	if m.CurrentAmountUSD != 101 {
		t.Errorf("current amount = %d, want 101", m.CurrentAmountUSD)
	}
	if m.Rolling24hVolumeUSD != 10_000 {
		t.Errorf("24h volume = %d, want 10000", m.Rolling24hVolumeUSD)
	}
	if m.Rolling7dVolumeUSD != 25_500 {
		t.Errorf("7d volume = %d, want 25500", m.Rolling7dVolumeUSD)
	}
	if m.Rolling30dAvgDailyVolumeUSD != 10_000 {
		t.Errorf("30d average = %d, want 10000", m.Rolling30dAvgDailyVolumeUSD)
	}
	if m.Rolling7dCount != 9 || m.Rolling1hCount != 2 || m.Rolling7dNighttimeCount != 4 {
		t.Errorf("counts = %d/%d/%d, want 9/2/4",
			m.Rolling7dCount, m.Rolling1hCount, m.Rolling7dNighttimeCount)
	}
	if m.AccountAgeDays != 12 {
		t.Errorf("account age = %d, want 12", m.AccountAgeDays)
	}
}

// Sums are carried in minor units all the way through SQL and truncated
// once, here — so a history of sub-dollar transfers reads as its real
// total instead of collapsing to zero.
func TestNewAccountMetricsTruncatesOnlyOnce(t *testing.T) {
	m := compliance.NewAccountMetrics(uuid.New(), 99, compliance.Volumes{
		Volume24hMinor: 9_900, // 100 transfers of $0.99
	}, 0)

	if m.Rolling24hVolumeUSD != 99 {
		t.Errorf("24h volume = %d, want 99", m.Rolling24hVolumeUSD)
	}
	// The transfer itself is worth $0.99, which is $0 of whole dollars —
	// the thresholds it feeds are all far above a dollar.
	if m.CurrentAmountUSD != 0 {
		t.Errorf("current amount = %d, want 0", m.CurrentAmountUSD)
	}
}

// A brand-new subject has no history at all; every rule still runs, on
// zeros — and the spike rules explicitly do not fire on a zero average.
func TestNewAccountMetricsWithNoHistory(t *testing.T) {
	m := compliance.NewAccountMetrics(uuid.New(), 5_000, compliance.Volumes{}, 0)

	if m.Rolling30dAvgDailyVolumeUSD != 0 {
		t.Errorf("30d average = %d, want 0", m.Rolling30dAvgDailyVolumeUSD)
	}
	if m.CurrentAmountUSD != 50 {
		t.Errorf("current amount = %d, want 50", m.CurrentAmountUSD)
	}
}
