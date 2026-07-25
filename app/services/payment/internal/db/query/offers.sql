-- name: OfferTransfer :many
-- One broadcast round; zero rows = the transfer left pending_match.
SELECT * FROM payments_ext.offer_transfer(
    sqlc.arg(transfer_id)::uuid,
    sqlc.arg(agent_profile_ids)::uuid[],
    sqlc.arg(scores)::int[],
    sqlc.arg(expires_at)
);

-- name: AcceptOffer :one
-- THE race — zero rows = lost it, offer gone, or insufficient available.
SELECT * FROM payments_ext.accept_offer(
    sqlc.arg(offer_id)::uuid,
    sqlc.arg(agent_profile_id)::uuid,
    sqlc.arg(actor)::uuid
);

-- name: DeclineOffer :one
SELECT * FROM payments_ext.decline_offer(
    sqlc.arg(offer_id)::uuid,
    sqlc.arg(agent_profile_id)::uuid
);

-- name: ExpireDueOffers :many
SELECT * FROM payments_ext.expire_due_offers(sqlc.arg(transfer_id)::uuid);

-- name: RequeueUnmatchedTransfer :one
-- Zero rows = an accept won the round (or the transfer already left).
SELECT * FROM payments_ext.requeue_unmatched_transfer(sqlc.arg(transfer_id)::uuid);

-- name: GetOffer :one
SELECT id, transfer_fk, agent_profile_fk, match_round, score, status, expires_at, decided_at, created_at, updated_at
FROM payments_ext.offers
WHERE id = sqlc.arg(id)::uuid;

-- name: ListAgentOffers :many
-- The agent inbox: live offers with the money summary, soonest-expiring
-- first. Status optional (NULL = live inbox default handled by caller).
SELECT id, transfer_fk, agent_profile_fk, match_round, score, status, expires_at, decided_at, created_at, transfer_reference, from_currency, from_operator, from_amount, to_currency, recipient_operator, recipient_city, to_amount, total_amount, hold_amount, note
FROM payments_ext.agent_offers
WHERE agent_profile_fk = sqlc.arg(agent_profile_id)::uuid
  AND (sqlc.narg(status)::offer_status IS NULL OR status = sqlc.narg(status)::offer_status)
ORDER BY expires_at
LIMIT sqlc.arg(max_rows);
