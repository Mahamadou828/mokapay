-- name: AddCommission :one
-- Zero rows = the transfer already has its accrual (idempotent by
-- transfer_fk UNIQUE).
SELECT * FROM payments_ext.add_commission(
    sqlc.arg(transfer_id)::uuid,
    sqlc.arg(agent_profile_id)::uuid,
    sqlc.arg(fee_currency)::currency,
    sqlc.arg(fee_amount),
    sqlc.arg(agent_amount),
    sqlc.arg(platform_amount),
    sqlc.arg(guarantee_fund_amount),
    sqlc.arg(margin_currency)::currency,
    sqlc.arg(margin_amount),
    sqlc.arg(payable_amount),
    sqlc.arg(accounting_week)
);

-- name: GetCommissionByTransfer :one
SELECT id, transfer_fk, transfer_reference, agent_profile_fk, fee_currency, fee_amount,
       agent_amount, platform_amount, guarantee_fund_amount, margin_currency,
       margin_amount, payable_amount, accounting_week, batch_fk, status,
       created_at, updated_at
FROM payments_ext.commissions
WHERE transfer_fk = sqlc.arg(transfer_id)::uuid;

-- name: ListCommissions :many
-- All filters optional: NULL disables each.
SELECT id, transfer_fk, transfer_reference, agent_profile_fk, fee_currency, fee_amount,
       agent_amount, platform_amount, guarantee_fund_amount, margin_currency,
       margin_amount, payable_amount, accounting_week, batch_fk, status,
       created_at, updated_at
FROM payments_ext.commissions
WHERE (sqlc.narg(agent_profile_id)::uuid IS NULL OR agent_profile_fk = sqlc.narg(agent_profile_id)::uuid)
  AND (sqlc.narg(status)::commission_status IS NULL OR status = sqlc.narg(status)::commission_status)
  AND (sqlc.narg(accounting_week)::date IS NULL OR accounting_week = sqlc.narg(accounting_week)::date)
ORDER BY created_at DESC
LIMIT sqlc.arg(max_rows);

-- name: ListDueCommissions :many
-- The weekly run's worklist: every unpaid accrual grouped by agent and
-- accounting week, oldest week first. The cutoff excludes the week still
-- in progress — a week is only paid once it is closed.
SELECT agent_profile_fk,
       accounting_week,
       count(*)::bigint AS commission_count,
       sum(payable_amount)::bigint AS total_amount
FROM payments_ext.commissions
WHERE batch_fk IS NULL
  AND accounting_week < sqlc.arg(week_cutoff)::date
GROUP BY agent_profile_fk, accounting_week
ORDER BY accounting_week, agent_profile_fk;

-- name: AddCommissionBatch :one
-- Zero rows = nothing due, the total moved under the caller's feet, or the
-- week is already batched for this agent.
SELECT * FROM payments_ext.add_commission_batch(
    sqlc.arg(agent_profile_id)::uuid,
    sqlc.arg(accounting_week),
    sqlc.arg(total_amount),
    sqlc.arg(ledger_request_id)::uuid
);

-- name: ListCommissionBatches :many
-- All filters optional: NULL disables each.
SELECT id, agent_profile_fk, accounting_week, commission_count, total_amount,
       ledger_request_fk, ledger_request_ref, ledger_request_status, status,
       created_at, updated_at
FROM payments_ext.commission_batches
WHERE (sqlc.narg(agent_profile_id)::uuid IS NULL OR agent_profile_fk = sqlc.narg(agent_profile_id)::uuid)
  AND (sqlc.narg(status)::commission_status IS NULL OR status = sqlc.narg(status)::commission_status)
ORDER BY accounting_week DESC, created_at DESC
LIMIT sqlc.arg(max_rows);
