package usecase

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"mokapay.app/app/services/compliance/internal/business/evaluation"
)

// White-box unit tests: the rule engine is pure integer arithmetic over
// the injected snapshot and config — no DB, no clock, no network.

// testConfig mirrors the shipped defaults (internal/config.go).
func testConfig() Config {
	return Config{
		DailyLimitUSD:                 10_000,
		WeeklyLimitUSD:                10_000,
		StructuringMinCount:           3,
		SenderSpikeMult:               5,
		AgentSpikeMult:                3,
		BeneficiaryConcentrationCount: 10,
		HourlyVelocityCount:           5,
		NighttimeCount:                3,
		NewAccountDays:                1,
		NewAccountVolumeUSD:           500,
		RiskMedium:                    30,
		RiskHigh:                      60,
		RiskCritical:                  80,
		PointsLow:                     5,
		PointsMedium:                  15,
		PointsHigh:                    35,
	}
}

// quietSnapshot is a wholly unremarkable transfer: an established sender
// and agent, an ordinary recipient. Nothing in it may trip any rule — each
// test then moves exactly one metric.
func quietSnapshot() evaluation.Snapshot {
	account := evaluation.AccountMetrics{
		AccountID:                   uuid.New(),
		CurrentAmountUSD:            100,
		Rolling24hVolumeUSD:         100,
		Rolling7dVolumeUSD:          400,
		Rolling7dCount:              2,
		Rolling30dAvgDailyVolumeUSD: 100,
		Rolling1hCount:              1,
		Rolling7dNighttimeCount:     0,
		AccountAgeDays:              365,
	}
	agent := account
	agent.AccountID = uuid.New()

	return evaluation.Snapshot{
		TransferID: uuid.New(),
		Sender:     account,
		Agent:      new(agent),
		Recipient: evaluation.RecipientMetrics{
			Phone:             "+243810000000",
			DistinctSenders7d: 1,
		},
	}
}

// find returns the audit row of one rule; every rule must always produce
// exactly one, so a miss is a test failure, not a nil.
func find(t *testing.T, rows []evaluation.NewRuleEvaluation, code evaluation.RuleCode) evaluation.NewRuleEvaluation {
	t.Helper()
	for _, r := range rows {
		if r.RuleCode == code {
			return r
		}
	}
	t.Fatalf("rule %s produced no evaluation row", code)
	return evaluation.NewRuleEvaluation{}
}

func TestEvaluateLogsEveryRule(t *testing.T) {
	cfg := testConfig()
	snap := quietSnapshot()

	rows := Evaluate(cfg, snap)
	if len(rows) != len(rules()) {
		t.Fatalf("got %d rows, want one per rule (%d)", len(rows), len(rules()))
	}

	for _, r := range rows {
		if r.Triggered {
			t.Errorf("rule %s triggered on a quiet snapshot", r.RuleCode)
		}
		if r.Severity != nil {
			t.Errorf("rule %s: severity set on a non-triggered row", r.RuleCode)
		}
		if r.RiskPoints != 0 {
			t.Errorf("rule %s: %d risk points on a non-triggered row", r.RuleCode, r.RiskPoints)
		}
		if r.TransferID != snap.TransferID {
			t.Errorf("rule %s: transfer id %s, want %s", r.RuleCode, r.TransferID, snap.TransferID)
		}
		if !json.Valid(r.Inputs) {
			t.Errorf("rule %s: inputs are not valid JSON: %s", r.RuleCode, r.Inputs)
		}
	}
}

// The subject decides which identifier the audit row carries — the
// database's own CHECK constraint enforces the same pairing.
func TestEvaluateSubjectIdentifiers(t *testing.T) {
	snap := quietSnapshot()
	rows := Evaluate(testConfig(), snap)

	for _, r := range rows {
		switch r.SubjectType {
		case evaluation.SubjectSender:
			if r.AccountID == nil || *r.AccountID != snap.Sender.AccountID {
				t.Errorf("rule %s: sender row does not carry the sender account", r.RuleCode)
			}
			if r.RecipientPhone != nil {
				t.Errorf("rule %s: sender row carries a recipient phone", r.RuleCode)
			}
		case evaluation.SubjectAgent:
			if r.AccountID == nil || *r.AccountID != snap.Agent.AccountID {
				t.Errorf("rule %s: agent row does not carry the agent account", r.RuleCode)
			}
		case evaluation.SubjectRecipient:
			if r.RecipientPhone == nil || *r.RecipientPhone != snap.Recipient.Phone {
				t.Errorf("rule %s: recipient row does not carry the recipient phone", r.RuleCode)
			}
			if r.AccountID != nil {
				t.Errorf("rule %s: recipient row carries an account id", r.RuleCode)
			}
		}
	}
}

// A transfer evaluated before matching has no agent: the agent rules are
// skipped rather than logged against a subject that does not exist.
func TestEvaluateSkipsAgentRulesWithoutAnAgent(t *testing.T) {
	snap := quietSnapshot()
	snap.Agent = nil

	rows := Evaluate(testConfig(), snap)
	for _, r := range rows {
		if r.SubjectType == evaluation.SubjectAgent {
			t.Fatalf("rule %s evaluated an absent agent", r.RuleCode)
		}
	}
	if len(rows) != len(rules())-1 {
		t.Fatalf("got %d rows, want %d (every rule but the agent one)", len(rows), len(rules())-1)
	}
}

func TestDailyVolumeLimit(t *testing.T) {
	cfg := testConfig()

	tests := []struct {
		name      string
		volume24h int64
		want      bool
	}{
		{"below the limit", 9_999, false},
		{"exactly at the limit", 10_000, true},
		{"above the limit", 25_000, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			snap := quietSnapshot()
			snap.Sender.Rolling24hVolumeUSD = tc.volume24h
			// Keep the 30-day average high enough that the spike rule
			// stays quiet — this test is about one rule only.
			snap.Sender.Rolling30dAvgDailyVolumeUSD = tc.volume24h

			row := find(t, Evaluate(cfg, snap), evaluation.RuleDailyVolumeLimit)
			if row.Triggered != tc.want {
				t.Fatalf("triggered = %v, want %v", row.Triggered, tc.want)
			}
			if tc.want {
				if row.Severity == nil || *row.Severity != evaluation.SeverityHigh {
					t.Fatalf("severity = %v, want high", row.Severity)
				}
				if row.RiskPoints != cfg.PointsHigh {
					t.Fatalf("risk points = %d, want %d", row.RiskPoints, cfg.PointsHigh)
				}
			}
		})
	}
}

func TestWeeklyStructuring(t *testing.T) {
	cfg := testConfig()

	tests := []struct {
		name     string
		volume7d int64
		count7d  int32
		current  int64
		want     bool
	}{
		{"quiet week", 400, 2, 100, false},
		{"volume without the transfer count", 12_000, 2, 100, false},
		{"count without the volume", 900, 5, 100, false},
		// One big transfer over the daily limit is the daily-limit rule's
		// business, not structuring.
		{"one transfer above the daily limit", 12_000, 3, 12_000, false},
		{"fractionated under the daily limit", 12_000, 4, 900, true},
		{"exactly at both thresholds", 10_000, 3, 900, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			snap := quietSnapshot()
			snap.Sender.Rolling7dVolumeUSD = tc.volume7d
			snap.Sender.Rolling7dCount = tc.count7d
			snap.Sender.CurrentAmountUSD = tc.current

			row := find(t, Evaluate(cfg, snap), evaluation.RuleWeeklyStructuring)
			if row.Triggered != tc.want {
				t.Fatalf("triggered = %v, want %v", row.Triggered, tc.want)
			}
		})
	}
}

func TestSenderVolumeSpike(t *testing.T) {
	cfg := testConfig()

	tests := []struct {
		name      string
		avg30d    int64
		volume24h int64
		want      bool
	}{
		{"within the usual range", 100, 400, false},
		{"just under the multiplier", 100, 499, false},
		{"exactly at the multiplier", 100, 500, true},
		{"far above", 100, 5_000, true},
		// No history means no baseline to be a multiple of — the
		// new-account rule covers that case instead.
		{"no history", 0, 5_000, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			snap := quietSnapshot()
			snap.Sender.Rolling30dAvgDailyVolumeUSD = tc.avg30d
			snap.Sender.Rolling24hVolumeUSD = tc.volume24h

			row := find(t, Evaluate(cfg, snap), evaluation.RuleSenderVolumeSpike)
			if row.Triggered != tc.want {
				t.Fatalf("triggered = %v, want %v", row.Triggered, tc.want)
			}
			if tc.want && row.RiskPoints != cfg.PointsMedium {
				t.Fatalf("risk points = %d, want %d", row.RiskPoints, cfg.PointsMedium)
			}
		})
	}
}

func TestAgentVolumeSpike(t *testing.T) {
	cfg := testConfig()

	tests := []struct {
		name      string
		avg30d    int64
		volume24h int64
		want      bool
	}{
		{"a normal day", 1_000, 2_000, false},
		{"just under the multiplier", 1_000, 2_999, false},
		{"exactly at the multiplier", 1_000, 3_000, true},
		{"no history", 0, 9_000, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			snap := quietSnapshot()
			snap.Agent.Rolling30dAvgDailyVolumeUSD = tc.avg30d
			snap.Agent.Rolling24hVolumeUSD = tc.volume24h

			row := find(t, Evaluate(cfg, snap), evaluation.RuleAgentVolumeSpike)
			if row.Triggered != tc.want {
				t.Fatalf("triggered = %v, want %v", row.Triggered, tc.want)
			}
			if row.SubjectType != evaluation.SubjectAgent {
				t.Fatalf("subject = %s, want agent", row.SubjectType)
			}
		})
	}
}

func TestBeneficiaryConcentration(t *testing.T) {
	cfg := testConfig()

	tests := []struct {
		name    string
		senders int32
		want    bool
	}{
		{"a handful of senders", 4, false},
		{"one short of the threshold", 9, false},
		{"exactly at the threshold", 10, true},
		{"a collection account", 40, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			snap := quietSnapshot()
			snap.Recipient.DistinctSenders7d = tc.senders

			row := find(t, Evaluate(cfg, snap), evaluation.RuleBeneficiaryConcentration)
			if row.Triggered != tc.want {
				t.Fatalf("triggered = %v, want %v", row.Triggered, tc.want)
			}
			if row.SubjectType != evaluation.SubjectRecipient {
				t.Fatalf("subject = %s, want recipient", row.SubjectType)
			}
		})
	}
}

func TestHourlyVelocity(t *testing.T) {
	cfg := testConfig()

	tests := []struct {
		name  string
		count int32
		want  bool
	}{
		{"a couple of transfers", 2, false},
		{"one short of the threshold", 4, false},
		{"exactly at the threshold", 5, true},
		{"a burst", 20, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			snap := quietSnapshot()
			snap.Sender.Rolling1hCount = tc.count

			row := find(t, Evaluate(cfg, snap), evaluation.RuleHourlyVelocity)
			if row.Triggered != tc.want {
				t.Fatalf("triggered = %v, want %v", row.Triggered, tc.want)
			}
		})
	}
}

func TestNightTimeStructuring(t *testing.T) {
	cfg := testConfig()

	tests := []struct {
		name  string
		count int32
		want  bool
	}{
		{"never at night", 0, false},
		{"one short of the threshold", 2, false},
		{"exactly at the threshold", 3, true},
		{"a nightly habit", 12, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			snap := quietSnapshot()
			snap.Sender.Rolling7dNighttimeCount = tc.count

			row := find(t, Evaluate(cfg, snap), evaluation.RuleNightTimeStructuring)
			if row.Triggered != tc.want {
				t.Fatalf("triggered = %v, want %v", row.Triggered, tc.want)
			}
			// The weakest signal of the set: it must stay cheap, or it
			// drags every night-working sender towards an escalation.
			if tc.want && row.RiskPoints != cfg.PointsLow {
				t.Fatalf("risk points = %d, want %d", row.RiskPoints, cfg.PointsLow)
			}
		})
	}
}

func TestNewAccountHighVolume(t *testing.T) {
	cfg := testConfig()

	tests := []struct {
		name    string
		ageDays int32
		amount  int64
		want    bool
	}{
		{"established account, large transfer", 400, 5_000, false},
		{"brand-new account, small transfer", 0, 100, false},
		{"new account, exactly at the threshold", 1, 500, false},
		{"new account, just above the threshold", 1, 501, true},
		{"same-day account, large transfer", 0, 5_000, true},
		{"two-day-old account, large transfer", 2, 5_000, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			snap := quietSnapshot()
			snap.Sender.AccountAgeDays = tc.ageDays
			snap.Sender.CurrentAmountUSD = tc.amount

			row := find(t, Evaluate(cfg, snap), evaluation.RuleNewAccountHighVolume)
			if row.Triggered != tc.want {
				t.Fatalf("triggered = %v, want %v", row.Triggered, tc.want)
			}
		})
	}
}

func TestConfigPoints(t *testing.T) {
	cfg := testConfig()

	tests := []struct {
		severity evaluation.Severity
		want     int32
	}{
		{evaluation.SeverityLow, 5},
		{evaluation.SeverityMedium, 15},
		{evaluation.SeverityHigh, 35},
		{evaluation.Severity("bogus"), 0},
	}

	for _, tc := range tests {
		if got := cfg.Points(tc.severity); got != tc.want {
			t.Errorf("Points(%q) = %d, want %d", tc.severity, got, tc.want)
		}
	}
}

func TestConfigLevel(t *testing.T) {
	cfg := testConfig()

	tests := []struct {
		score int64
		want  evaluation.RiskLevel
	}{
		{0, evaluation.RiskLow},
		{29, evaluation.RiskLow},
		{30, evaluation.RiskMedium},
		{59, evaluation.RiskMedium},
		{60, evaluation.RiskHigh},
		{79, evaluation.RiskHigh},
		{80, evaluation.RiskCritical},
		{500, evaluation.RiskCritical},
	}

	for _, tc := range tests {
		if got := cfg.Level(tc.score); got != tc.want {
			t.Errorf("Level(%d) = %q, want %q", tc.score, got, tc.want)
		}
	}
}
