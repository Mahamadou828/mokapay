-- name: GetAccountRisk :one
-- The 90-day rolling score. Zero rows = the account has no evaluation in
-- the window, which the usecase reports as a score of 0.
SELECT account_nk, risk_score, last_evaluated_at
FROM compliance_ext.account_risk
WHERE account_nk = sqlc.arg(account_nk)::uuid;

-- name: GetDashboardCounts :one
SELECT open_alerts, under_review_alerts, escalated_alerts,
       high_severity_alerts, medium_severity_alerts, low_severity_alerts,
       flagged_transfers, open_cases, investigating_cases, sar_cases
FROM compliance_ext.dashboard_counts;
