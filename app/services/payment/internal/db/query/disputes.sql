-- name: OpenDispute :one
-- Zero rows = unknown transfer or a state that cannot be disputed.
SELECT * FROM payments_ext.open_dispute(
    sqlc.arg(transfer_id)::uuid,
    sqlc.arg(party)::dispute_party,
    sqlc.narg(opened_by)::uuid,
    sqlc.arg(reason)::dispute_reason,
    sqlc.narg(details)
);

-- name: GetDispute :one
SELECT id, transfer_fk, transfer_reference, client_nk, agent_profile_fk,
       opened_by_party, opened_by, reason, details, from_status, hold_amount,
       refund_currency, refund_amount, status, resolution, hold_outcome,
       resolution_note, deduction_fk, resolved_by, resolved_at,
       created_at, updated_at
FROM payments_ext.disputes
WHERE id = sqlc.arg(id)::uuid;

-- name: GetOpenDisputeByTransfer :one
SELECT id, transfer_fk, transfer_reference, client_nk, agent_profile_fk,
       opened_by_party, opened_by, reason, details, from_status, hold_amount,
       refund_currency, refund_amount, status, resolution, hold_outcome,
       resolution_note, deduction_fk, resolved_by, resolved_at,
       created_at, updated_at
FROM payments_ext.disputes
WHERE transfer_fk = sqlc.arg(transfer_id)::uuid AND status = 'open';

-- name: ListDisputes :many
-- All filters optional: NULL disables each.
SELECT id, transfer_fk, transfer_reference, client_nk, agent_profile_fk,
       opened_by_party, opened_by, reason, details, from_status, hold_amount,
       refund_currency, refund_amount, status, resolution, hold_outcome,
       resolution_note, deduction_fk, resolved_by, resolved_at,
       created_at, updated_at
FROM payments_ext.disputes
WHERE (sqlc.narg(status)::dispute_status IS NULL OR status = sqlc.narg(status)::dispute_status)
  AND (sqlc.narg(transfer_id)::uuid IS NULL OR transfer_fk = sqlc.narg(transfer_id)::uuid)
  AND (sqlc.narg(agent_profile_id)::uuid IS NULL OR agent_profile_fk = sqlc.narg(agent_profile_id)::uuid)
  AND (sqlc.narg(client_id)::uuid IS NULL OR client_nk = sqlc.narg(client_id)::uuid)
ORDER BY created_at DESC
LIMIT sqlc.arg(max_rows);

-- name: ResolveDisputeRelease :one
-- Zero rows = unknown dispute, already resolved, or the transfer left
-- `disputed` meanwhile.
SELECT * FROM payments_ext.resolve_dispute_release(
    sqlc.arg(id)::uuid,
    sqlc.arg(resolution)::dispute_resolution,
    sqlc.arg(terminal_status)::transfer_status,
    sqlc.arg(resolved_by)::uuid,
    sqlc.narg(note)
);

-- name: ResolveDisputeCapture :one
-- Zero rows = unknown dispute, already resolved, or the transfer left
-- `disputed` meanwhile.
SELECT * FROM payments_ext.resolve_dispute_capture(
    sqlc.arg(id)::uuid,
    sqlc.arg(resolved_by)::uuid,
    sqlc.narg(note)
);
