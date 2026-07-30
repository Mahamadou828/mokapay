package usecase

import "mokapay.app/app/services/compliance/internal/business/evaluation"

// Config carries every tunable of the rule engine. All of it is env
// config read at startup (internal/config.go): ops retune a threshold
// without a migration and without a code change, which is why no
// threshold is hardcoded in a rule, a view or a procedure.
//
// Amounts are WHOLE USD, matching the snapshot the caller sends.
type Config struct {
	DailyLimitUSD                 int64
	WeeklyLimitUSD                int64
	StructuringMinCount           int32
	SenderSpikeMult               int32
	AgentSpikeMult                int32
	BeneficiaryConcentrationCount int32
	HourlyVelocityCount           int32
	NighttimeCount                int32
	NewAccountDays                int32
	NewAccountVolumeUSD           int64

	// Score bands: a score >= RiskCritical is critical, >= RiskHigh is
	// high, >= RiskMedium is medium, anything below is low.
	RiskMedium   int64
	RiskHigh     int64
	RiskCritical int64

	// Points one triggered rule contributes, by severity.
	PointsLow    int32
	PointsMedium int32
	PointsHigh   int32
}

// Points returns the risk points a triggered rule of this severity
// contributes. An unknown severity contributes nothing — a scoring table
// never invents weight for a value it does not know.
func (c Config) Points(s evaluation.Severity) int32 {
	switch s {
	case evaluation.SeverityLow:
		return c.PointsLow
	case evaluation.SeverityMedium:
		return c.PointsMedium
	case evaluation.SeverityHigh:
		return c.PointsHigh
	}
	return 0
}

// Level buckets a risk score into its band.
func (c Config) Level(score int64) evaluation.RiskLevel {
	switch {
	case score >= c.RiskCritical:
		return evaluation.RiskCritical
	case score >= c.RiskHigh:
		return evaluation.RiskHigh
	case score >= c.RiskMedium:
		return evaluation.RiskMedium
	}
	return evaluation.RiskLow
}
