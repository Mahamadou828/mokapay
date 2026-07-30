-- The risk score, COMPUTED — never a stored running total (a stored total
-- drifts the moment one write is missed, and cannot be recomputed from
-- history). Points were snapshotted on each evaluation, so re-tuning the
-- severity table never rewrites past scores.
--
-- The 90-day lookback is a schema-level constant on purpose: it changes
-- through a migration, like any other structural choice. The score→level
-- bucketing is NOT here — it is applied in the Go usecase from env config.
CREATE OR REPLACE VIEW compliance_ext.account_risk AS
SELECT account_nk,
       SUM(risk_points)::BIGINT AS risk_score,
       MAX(evaluated_at)::TIMESTAMPTZ AS last_evaluated_at
FROM rule_evaluations
WHERE account_nk IS NOT NULL
  AND evaluated_at > now() - INTERVAL '90 days'
GROUP BY account_nk;
