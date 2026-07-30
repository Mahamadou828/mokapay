package evaluation

import "github.com/google/uuid"

// The metrics snapshot. Compliance owns no transfer history: every
// rolling aggregate a rule needs arrives on the request, computed by the
// service that owns the data (payment, over its own indexed transfers).
// That keeps this service a stateless-per-call threshold engine and makes
// the audit row literally record what was checked.
//
// All amounts are WHOLE USD, not minor units — the caller converts, so
// the configured thresholds read as the dollar figures ops signed off on.

// AccountMetrics is the snapshot for one account-backed subject (the
// sender or the assigned agent).
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
// off-platform — a phone number, never an account.
type RecipientMetrics struct {
	Phone             string
	DistinctSenders7d int32
}

// Snapshot is one whole evaluation input. Agent is nil when the transfer
// has no agent yet (evaluated before matching) — its rules are then not
// evaluated at all: there is no subject to record them against.
type Snapshot struct {
	TransferID uuid.UUID
	Sender     AccountMetrics
	Agent      *AccountMetrics
	Recipient  RecipientMetrics
}
