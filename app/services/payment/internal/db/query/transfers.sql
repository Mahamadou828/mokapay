-- name: AddTransfer :one
-- Money columns are stamped once here (rate locked at create); the CHECK
-- constraints veto a bad payload atomically.
SELECT * FROM payments_ext.add_transfer(
    sqlc.arg(client_nk)::uuid,
    sqlc.narg(beneficiary_id)::uuid,
    sqlc.arg(recipient_name),
    sqlc.arg(recipient_phone),
    sqlc.arg(recipient_operator)::payment_operator,
    sqlc.arg(from_currency)::currency,
    sqlc.arg(from_operator)::payment_operator,
    sqlc.arg(from_amount),
    sqlc.arg(to_currency)::currency,
    sqlc.arg(to_amount),
    sqlc.arg(fee_pct)::numeric,
    sqlc.arg(fee_amount),
    sqlc.arg(margin_amount),
    sqlc.arg(hold_amount),
    sqlc.narg(rate)::numeric,
    sqlc.narg(recipient_city),
    sqlc.narg(note)
);

-- name: GetTransfer :one
SELECT id, reference, client_nk, beneficiary_fk, recipient_name, recipient_phone, recipient_operator, recipient_city, from_currency, from_operator, from_amount, to_currency, to_amount, fee_pct, fee_amount, margin_amount, total_amount, rate, hold_amount, note, status, agent_profile_fk, match_round, proof_client_nk, proof_agent_nk, created_at, updated_at
FROM payments_ext.transfers
WHERE id = sqlc.arg(id)::uuid;

-- name: ListTransfers :many
-- Optional filters: NULL disables each. Serves the client history, the
-- agent history and the ops listing from one query.
SELECT id, reference, client_nk, beneficiary_fk, recipient_name, recipient_phone, recipient_operator, recipient_city, from_currency, from_operator, from_amount, to_currency, to_amount, fee_pct, fee_amount, margin_amount, total_amount, rate, hold_amount, note, status, agent_profile_fk, match_round, proof_client_nk, proof_agent_nk, created_at, updated_at
FROM payments_ext.transfers
WHERE (sqlc.narg(client_nk)::uuid IS NULL OR client_nk = sqlc.narg(client_nk)::uuid)
  AND (sqlc.narg(agent_profile_id)::uuid IS NULL OR agent_profile_fk = sqlc.narg(agent_profile_id)::uuid)
  AND (sqlc.narg(status)::transfer_status IS NULL OR status = sqlc.narg(status)::transfer_status)
ORDER BY created_at DESC
LIMIT sqlc.arg(max_rows);

-- name: ListTransferEvents :many
-- The C13 timeline, oldest first.
SELECT id, transfer_fk, actor_nk, from_status, to_status, reason, created_at
FROM payments_ext.transfer_events
WHERE transfer_fk = sqlc.arg(transfer_id)::uuid
ORDER BY created_at;

-- name: ListStalePendingMatch :many
-- The rescue sweep: pending_match transfers whose last activity is older
-- than the cutoff — their match job died somewhere; re-enqueue.
SELECT id
FROM payments_ext.transfers
WHERE status = 'pending_match' AND updated_at < sqlc.arg(cutoff)
ORDER BY created_at
LIMIT sqlc.arg(max_rows);

-- name: MarkTransferClientPaid :one
-- Zero rows = not in pending_client_transfer (redelivered proof — fine).
SELECT * FROM payments_ext.mark_transfer_client_paid(
    sqlc.arg(id)::uuid,
    sqlc.arg(proof_file_id)::uuid,
    sqlc.narg(actor)::uuid
);

-- name: MarkTransferAgentPaid :one
SELECT * FROM payments_ext.mark_transfer_agent_paid(
    sqlc.arg(id)::uuid,
    sqlc.arg(proof_file_id)::uuid,
    sqlc.narg(actor)::uuid
);

-- name: CompleteTransfer :one
-- Confirm or auto-confirm: → done + hold release, exactly once.
SELECT * FROM payments_ext.complete_transfer(
    sqlc.arg(id)::uuid,
    sqlc.narg(actor)::uuid,
    sqlc.narg(reason)
);

-- name: CancelTransferPreAccept :one
SELECT * FROM payments_ext.cancel_transfer_pre_accept(
    sqlc.arg(id)::uuid,
    sqlc.arg(client_nk)::uuid,
    sqlc.narg(reason)
);

-- name: CancelTransferAdmin :one
SELECT * FROM payments_ext.cancel_transfer_admin(
    sqlc.arg(id)::uuid,
    sqlc.arg(actor)::uuid,
    sqlc.arg(reason)
);

-- name: CancelTransferTimeout :one
SELECT * FROM payments_ext.cancel_transfer_timeout(
    sqlc.arg(id)::uuid,
    sqlc.arg(reason)
);

-- name: ExpireTransfer :one
SELECT * FROM payments_ext.expire_transfer(
    sqlc.arg(id)::uuid,
    sqlc.arg(reason)
);
