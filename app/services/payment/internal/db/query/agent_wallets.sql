-- name: AddAgentWallet :one
-- 23505 on agent_wallets_pair_uq = pair already declared.
SELECT * FROM payments_ext.add_agent_wallet(
    sqlc.arg(agent_profile_id)::uuid,
    sqlc.arg(operator)::payment_operator,
    sqlc.arg(currency)::currency,
    sqlc.arg(msisdn)
);

-- name: ListAgentWallets :many
SELECT id, agent_profile_fk, operator, currency, msisdn, created_at, updated_at
FROM payments_ext.agent_wallets
WHERE agent_profile_fk = sqlc.arg(agent_profile_id)::uuid
ORDER BY created_at;

-- name: GetAgentWalletByPair :one
-- The payment-details lookup: the wallet the client pays after a match.
SELECT id, agent_profile_fk, operator, currency, msisdn, created_at, updated_at
FROM payments_ext.agent_wallets
WHERE agent_profile_fk = sqlc.arg(agent_profile_id)::uuid
  AND operator = sqlc.arg(operator)::payment_operator
  AND currency = sqlc.arg(currency)::currency;

-- name: DeleteAgentWallet :exec
SELECT payments_ext.delete_agent_wallet(
    sqlc.arg(id)::uuid,
    sqlc.arg(agent_profile_id)::uuid
);
