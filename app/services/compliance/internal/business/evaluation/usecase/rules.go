package usecase

import (
	"encoding/json"

	"mokapay.app/app/services/compliance/internal/business/evaluation"
)

// The fixed AML rule set. Two invariants hold for every rule:
//
//  1. it always runs and always produces an audit row — a rule that did
//     not fire is evidence too ("we checked, it was clean"), which is the
//     whole point of a provable trail;
//  2. it never blocks anything. A triggered rule opens an alert for a
//     human; money keeps moving.
//
// Thresholds come from env config (Config), so ops retune them without a
// release. Severity is a property of the rule, not of the reading.

// result is what one rule reports: whether it fired, and the exact inputs
// it looked at — the inputs are recorded verbatim in the audit row.
type result struct {
	triggered bool
	inputs    map[string]any
}

// rule is one check of the set.
type rule struct {
	code     evaluation.RuleCode
	subject  evaluation.SubjectType
	severity evaluation.Severity // the severity carried when it fires
	eval     func(cfg Config, snap evaluation.Snapshot) result
}

// rules returns the set in evaluation order. Order is stable so the audit
// rows of one transfer always read the same way.
func rules() []rule {
	return []rule{
		// Volume over a rolling day above the flat platform limit. The
		// single loudest signal, so: high.
		{
			code:     evaluation.RuleDailyVolumeLimit,
			subject:  evaluation.SubjectSender,
			severity: evaluation.SeverityHigh,
			eval: func(cfg Config, snap evaluation.Snapshot) result {
				m := snap.Sender
				return result{
					triggered: m.Rolling24hVolumeUSD >= cfg.DailyLimitUSD,
					inputs: map[string]any{
						"rolling_24h_volume_usd": m.Rolling24hVolumeUSD,
						"daily_limit_usd":        cfg.DailyLimitUSD,
					},
				}
			},
		},
		// Structuring (smurfing): the week's volume clears the weekly
		// limit through several transfers, while this one stays under the
		// daily limit. All three conditions together are the pattern —
		// the volume alone is the rule above, the count alone is normal
		// business.
		{
			code:     evaluation.RuleWeeklyStructuring,
			subject:  evaluation.SubjectSender,
			severity: evaluation.SeverityHigh,
			eval: func(cfg Config, snap evaluation.Snapshot) result {
				m := snap.Sender
				triggered := m.Rolling7dVolumeUSD >= cfg.WeeklyLimitUSD &&
					int64(m.Rolling7dCount) >= int64(cfg.StructuringMinCount) &&
					m.CurrentAmountUSD < cfg.DailyLimitUSD
				return result{
					triggered: triggered,
					inputs: map[string]any{
						"rolling_7d_volume_usd": m.Rolling7dVolumeUSD,
						"rolling_7d_count":      m.Rolling7dCount,
						"current_amount_usd":    m.CurrentAmountUSD,
						"weekly_limit_usd":      cfg.WeeklyLimitUSD,
						"structuring_min_count": cfg.StructuringMinCount,
						"daily_limit_usd":       cfg.DailyLimitUSD,
					},
				}
			},
		},
		// A sender moving a multiple of their own 30-day daily average.
		// Relative to their own history, so no KYC tier is needed — and a
		// brand-new sender (no average) cannot trip it.
		{
			code:     evaluation.RuleSenderVolumeSpike,
			subject:  evaluation.SubjectSender,
			severity: evaluation.SeverityMedium,
			eval: func(cfg Config, snap evaluation.Snapshot) result {
				m := snap.Sender
				triggered := m.Rolling30dAvgDailyVolumeUSD > 0 &&
					m.Rolling24hVolumeUSD >= m.Rolling30dAvgDailyVolumeUSD*int64(cfg.SenderSpikeMult)
				return result{
					triggered: triggered,
					inputs: map[string]any{
						"rolling_24h_volume_usd":           m.Rolling24hVolumeUSD,
						"rolling_30d_avg_daily_volume_usd": m.Rolling30dAvgDailyVolumeUSD,
						"sender_spike_mult":                cfg.SenderSpikeMult,
					},
				}
			},
		},
		// The same shape on the payout side, with a tighter multiplier:
		// an agent's throughput is their job, so a spike has to be
		// sharper before it means anything.
		{
			code:     evaluation.RuleAgentVolumeSpike,
			subject:  evaluation.SubjectAgent,
			severity: evaluation.SeverityMedium,
			eval: func(cfg Config, snap evaluation.Snapshot) result {
				m := *snap.Agent
				triggered := m.Rolling30dAvgDailyVolumeUSD > 0 &&
					m.Rolling24hVolumeUSD >= m.Rolling30dAvgDailyVolumeUSD*int64(cfg.AgentSpikeMult)
				return result{
					triggered: triggered,
					inputs: map[string]any{
						"rolling_24h_volume_usd":           m.Rolling24hVolumeUSD,
						"rolling_30d_avg_daily_volume_usd": m.Rolling30dAvgDailyVolumeUSD,
						"agent_spike_mult":                 cfg.AgentSpikeMult,
					},
				}
			},
		},
		// Many unrelated senders funnelling to one recipient in a week —
		// the classic collection-account shape.
		{
			code:     evaluation.RuleBeneficiaryConcentration,
			subject:  evaluation.SubjectRecipient,
			severity: evaluation.SeverityHigh,
			eval: func(cfg Config, snap evaluation.Snapshot) result {
				m := snap.Recipient
				return result{
					triggered: int64(m.DistinctSenders7d) >= int64(cfg.BeneficiaryConcentrationCount),
					inputs: map[string]any{
						"distinct_senders_7d":             m.DistinctSenders7d,
						"beneficiary_concentration_count": cfg.BeneficiaryConcentrationCount,
					},
				}
			},
		},
		// Burst behaviour inside one hour: automation or a rushed
		// break-up of one amount.
		{
			code:     evaluation.RuleHourlyVelocity,
			subject:  evaluation.SubjectSender,
			severity: evaluation.SeverityMedium,
			eval: func(cfg Config, snap evaluation.Snapshot) result {
				m := snap.Sender
				return result{
					triggered: int64(m.Rolling1hCount) >= int64(cfg.HourlyVelocityCount),
					inputs: map[string]any{
						"rolling_1h_count":      m.Rolling1hCount,
						"hourly_velocity_count": cfg.HourlyVelocityCount,
					},
				}
			},
		},
		// Repeated night-time activity. Weak on its own — plenty of
		// legitimate senders work nights — so: low. It earns its keep by
		// adding points alongside other signals.
		{
			code:     evaluation.RuleNightTimeStructuring,
			subject:  evaluation.SubjectSender,
			severity: evaluation.SeverityLow,
			eval: func(cfg Config, snap evaluation.Snapshot) result {
				m := snap.Sender
				return result{
					triggered: int64(m.Rolling7dNighttimeCount) >= int64(cfg.NighttimeCount),
					inputs: map[string]any{
						"rolling_7d_nighttime_count": m.Rolling7dNighttimeCount,
						"nighttime_count":            cfg.NighttimeCount,
					},
				}
			},
		},
		// A day-old account moving real money: the mule-account shape.
		{
			code:     evaluation.RuleNewAccountHighVolume,
			subject:  evaluation.SubjectSender,
			severity: evaluation.SeverityHigh,
			eval: func(cfg Config, snap evaluation.Snapshot) result {
				m := snap.Sender
				triggered := int64(m.AccountAgeDays) <= int64(cfg.NewAccountDays) &&
					m.CurrentAmountUSD > cfg.NewAccountVolumeUSD
				return result{
					triggered: triggered,
					inputs: map[string]any{
						"account_age_days":       m.AccountAgeDays,
						"current_amount_usd":     m.CurrentAmountUSD,
						"new_account_days":       cfg.NewAccountDays,
						"new_account_volume_usd": cfg.NewAccountVolumeUSD,
					},
				}
			},
		},
	}
}

// Evaluate runs the whole rule set against the snapshot and returns the
// audit rows to append, in rule order. Pure: no I/O, no clock, no
// randomness — the same snapshot and config always produce the same rows.
//
// A rule whose subject is absent from the snapshot (the agent rules on a
// not-yet-matched transfer) is skipped rather than logged: an audit row
// with no subject would violate the table's own subject constraint, and
// "we checked an agent that does not exist" is not a fact worth keeping.
func Evaluate(cfg Config, snap evaluation.Snapshot) []evaluation.NewRuleEvaluation {
	out := make([]evaluation.NewRuleEvaluation, 0, len(rules()))

	for _, r := range rules() {
		if r.subject == evaluation.SubjectAgent && snap.Agent == nil {
			continue
		}

		res := r.eval(cfg, snap)

		row := evaluation.NewRuleEvaluation{
			TransferID:  snap.TransferID,
			RuleCode:    r.code,
			SubjectType: r.subject,
			Triggered:   res.triggered,
			Inputs:      encodeInputs(res.inputs),
		}
		switch r.subject {
		case evaluation.SubjectSender:
			row.AccountID = new(snap.Sender.AccountID)
		case evaluation.SubjectAgent:
			row.AccountID = new(snap.Agent.AccountID)
		case evaluation.SubjectRecipient:
			row.RecipientPhone = new(snap.Recipient.Phone)
		}
		if res.triggered {
			row.Severity = new(r.severity)
			row.RiskPoints = cfg.Points(r.severity)
		}

		out = append(out, row)
	}

	return out
}

// encodeInputs serialises the audit inputs. A map of scalars cannot fail
// to marshal; if it somehow did, an empty object still satisfies the
// column's NOT NULL and the row (which is the evidence) is kept.
func encodeInputs(in map[string]any) []byte {
	raw, err := json.Marshal(in)
	if err != nil {
		return []byte(`{}`)
	}
	return raw
}
