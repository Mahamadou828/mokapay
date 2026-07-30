-- name: AddRuleEvaluation :one
-- Appends the audit row and, when triggered, opens its paired alert in the
-- same statement.
SELECT * FROM compliance_ext.add_rule_evaluation(
    sqlc.arg(transfer_nk)::uuid,
    sqlc.arg(rule_code)::compliance_rule_code,
    sqlc.arg(subject_type)::compliance_subject_type,
    sqlc.arg(triggered),
    sqlc.arg(inputs),
    sqlc.narg(account_nk)::uuid,
    sqlc.narg(recipient_phone),
    sqlc.narg(severity)::compliance_severity,
    sqlc.arg(risk_points)
);

-- name: ListRuleEvaluationsByTransfer :many
-- Everything that was checked on one transfer, newest first.
SELECT id, transfer_nk, rule_code, subject_type, account_nk, recipient_phone,
       triggered, severity, risk_points, inputs, evaluated_at
FROM compliance_ext.rule_evaluations
WHERE transfer_nk = sqlc.arg(transfer_nk)::uuid
ORDER BY evaluated_at DESC;

-- name: GetRuleEvaluation :one
SELECT id, transfer_nk, rule_code, subject_type, account_nk, recipient_phone,
       triggered, severity, risk_points, inputs, evaluated_at
FROM compliance_ext.rule_evaluations
WHERE id = sqlc.arg(id)::uuid;
