-- name: GetAlert :one
SELECT id, rule_evaluation_fk, status, assignee_nk, resolution_note,
       rule_code, subject_type, severity, transfer_nk, account_nk,
       recipient_phone, risk_points, evaluated_at, created_at, updated_at
FROM compliance_ext.alerts
WHERE id = sqlc.arg(id)::uuid;

-- name: ListAlerts :many
-- All filters optional: a NULL argument disables its filter.
SELECT id, rule_evaluation_fk, status, assignee_nk, resolution_note,
       rule_code, subject_type, severity, transfer_nk, account_nk,
       recipient_phone, risk_points, evaluated_at, created_at, updated_at
FROM compliance_ext.alerts
WHERE (sqlc.narg(status)::compliance_alert_status IS NULL OR status = sqlc.narg(status)::compliance_alert_status)
  AND (sqlc.narg(severity)::compliance_severity IS NULL OR severity = sqlc.narg(severity)::compliance_severity)
  AND (sqlc.narg(account_nk)::uuid IS NULL OR account_nk = sqlc.narg(account_nk)::uuid)
ORDER BY created_at DESC
LIMIT sqlc.arg(max_rows);

-- name: UpdateAlertStatus :one
-- Zero rows = unknown or soft-deleted id.
SELECT * FROM compliance_ext.update_alert_status(
    sqlc.arg(id)::uuid,
    sqlc.arg(status)::compliance_alert_status,
    sqlc.narg(assignee_nk)::uuid,
    sqlc.narg(resolution_note)
);
