// Package compliance holds payment's side of the AML integration: the
// metrics snapshot the compliance service is fed on every completed
// transfer. Compliance owns no transfer history — it is a stateless
// threshold engine over whatever snapshot it is given — so every rolling
// aggregate a rule needs is computed here, over payment's own indexed
// transfers.
//
// Nothing in this package can block a transfer: the snapshot is assembled
// after the completion committed, inside a River job.
package compliance

import (
	"errors"

	"github.com/google/uuid"
)

// ErrAccountNotFound is returned by an Accounts lookup that no longer
// resolves — an account id in payment with no row in user.
var ErrAccountNotFound = errors.New("account not found")

// minorUnitsPerUSD is the scale of every USD amount payment stores
// (hold_amount and friends are cents). Compliance thresholds are whole
// dollars, so the snapshot converts once, here.
const minorUnitsPerUSD = 100

// avgWindowDays is the denominator of the 30-day daily average — the
// window the volume-spike rules compare against.
const avgWindowDays = 30

// Volumes is one subject's raw rolling aggregate, exactly as the database
// counts it: money in USD minor units, counts as counts, windows relative
// to the query's now().
type Volumes struct {
	Volume24hMinor   int64
	Volume7dMinor    int64
	Count7d          int32
	Volume30dMinor   int64
	Count1h          int32
	Count7dNighttime int32
}

// AccountMetrics is the snapshot for one account-backed subject (the
// sender or the assigned agent), in the units the contract asks for:
// WHOLE USD, never minor units.
type AccountMetrics struct {
	AccountID                   uuid.UUID
	CurrentAmountUSD            int64
	Rolling24hVolumeUSD         int64
	Rolling7dVolumeUSD          int64
	Rolling7dCount              int32
	Rolling30dAvgDailyVolumeUSD int64
	Rolling1hCount              int32
	Rolling7dNighttimeCount     int32
	AccountAgeDays              int32
}

// RecipientMetrics is the snapshot for the payout side. Recipients are
// off-platform: a phone number, never an account.
type RecipientMetrics struct {
	Phone             string
	DistinctSenders7d int32
}

// Snapshot is one whole evaluation input. Agent is nil when the transfer
// has no assigned agent — the agent rules are then simply not evaluated.
type Snapshot struct {
	TransferID uuid.UUID
	Sender     AccountMetrics
	Agent      *AccountMetrics
	Recipient  RecipientMetrics
}

// NewAccountMetrics converts one subject's raw volumes into the whole-USD
// contract shape. currentMinor is the evaluated transfer's own USD value
// (its hold amount — the client's full total whatever the corridor).
//
// Truncation is deliberate and applied as late as possible: sums are
// carried in minor units through SQL and divided once here, so a hundred
// $0.99 transfers read as $99, not as zero.
func NewAccountMetrics(accountID uuid.UUID, currentMinor int64, v Volumes, ageDays int32) AccountMetrics {
	return AccountMetrics{
		AccountID:                   accountID,
		CurrentAmountUSD:            usdFromMinor(currentMinor),
		Rolling24hVolumeUSD:         usdFromMinor(v.Volume24hMinor),
		Rolling7dVolumeUSD:          usdFromMinor(v.Volume7dMinor),
		Rolling7dCount:              v.Count7d,
		Rolling30dAvgDailyVolumeUSD: usdFromMinor(v.Volume30dMinor / avgWindowDays),
		Rolling1hCount:              v.Count1h,
		Rolling7dNighttimeCount:     v.Count7dNighttime,
		AccountAgeDays:              ageDays,
	}
}

// usdFromMinor converts USD minor units to whole dollars.
func usdFromMinor(minor int64) int64 {
	return minor / minorUnitsPerUSD
}
