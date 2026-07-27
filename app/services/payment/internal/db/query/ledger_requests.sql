-- name: AddLedgerRequest :one
-- Zero rows = infeasible (insufficient available funds, guarantee floor,
-- or ineligible/unknown profile). 23505 on the reference index = the
-- entity already has a live-or-settled request; re-read with
-- GetLedgerRequestByReference.
SELECT * FROM payments_ext.add_ledger_request(
    sqlc.arg(agent_profile_id)::uuid,
    sqlc.arg(type)::ledger_request_type,
    sqlc.arg(direction)::ledger_direction,
    sqlc.arg(amount),
    sqlc.arg(created_by)::uuid,
    sqlc.arg(status)::ledger_request_status,
    sqlc.narg(reference_entity_type)::ledger_reference_type,
    sqlc.narg(reference_entity_id)::uuid,
    sqlc.narg(reason)
);

-- name: GetLedgerRequest :one
SELECT id, ref, agent_profile_fk, type, direction, amount, status, reference_entity_type, reference_entity_id, proof_file_nk, reason, created_by, decided_by, decided_at, created_at, updated_at
FROM payments_ext.ledger_requests
WHERE id = sqlc.arg(id)::uuid;

-- name: GetLedgerRequestByReference :one
-- The enrollment/transfer linkage lookup: the live-or-settled request of
-- the entity (rejected ones don't count — the slot is free again).
SELECT id, ref, agent_profile_fk, type, direction, amount, status, reference_entity_type, reference_entity_id, proof_file_nk, reason, created_by, decided_by, decided_at, created_at, updated_at
FROM payments_ext.ledger_requests
WHERE reference_entity_type = sqlc.arg(reference_entity_type)::ledger_reference_type
  AND reference_entity_id = sqlc.arg(reference_entity_id)::uuid
  AND status <> 'rejected';

-- name: ListLedgerRequests :many
-- All filters optional: NULL disables each.
SELECT id, ref, agent_profile_fk, type, direction, amount, status, reference_entity_type, reference_entity_id, proof_file_nk, reason, created_by, decided_by, decided_at, created_at, updated_at
FROM payments_ext.ledger_requests
WHERE (sqlc.narg(status)::ledger_request_status IS NULL OR status = sqlc.narg(status)::ledger_request_status)
  AND (sqlc.narg(type)::ledger_request_type IS NULL OR type = sqlc.narg(type)::ledger_request_type)
  AND (sqlc.narg(agent_profile_id)::uuid IS NULL OR agent_profile_fk = sqlc.narg(agent_profile_id)::uuid)
ORDER BY created_at DESC;

-- name: UpdateLedgerRequestStatus :one
-- Zero rows = unknown id or not a forward pending→pending move.
SELECT * FROM payments_ext.update_ledger_request_status(
    sqlc.arg(id)::uuid,
    sqlc.arg(status)::ledger_request_status,
    sqlc.narg(proof_file_nk)::uuid
);

-- name: RejectLedgerRequest :one
-- Zero rows = unknown id or already terminal.
SELECT * FROM payments_ext.reject_ledger_request(
    sqlc.arg(id)::uuid,
    sqlc.arg(decided_by)::uuid,
    sqlc.narg(reason)
);

-- name: ApproveLedgerRequest :one
-- Zero rows = unknown id or not pending_admin_approval (a done request
-- means a retried approval — the caller reads it back and moves on).
SELECT * FROM payments_ext.approve_ledger_request(
    sqlc.arg(id)::uuid,
    sqlc.arg(decided_by)::uuid
);

-- name: ListLedgerLines :many
-- The agent statement, newest first.
SELECT id, agent_profile_fk, ledger_request_fk, amount, balance_after, created_at
FROM payments_ext.ledger_lines
WHERE agent_profile_fk = sqlc.arg(agent_profile_id)::uuid
ORDER BY created_at DESC
LIMIT sqlc.arg(max_rows);
