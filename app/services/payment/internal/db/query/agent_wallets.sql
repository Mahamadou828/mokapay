-- name: AddAgentWallet :one
-- 23505 on agent_wallets_operator_uq = operator already declared.
SELECT * FROM payments_ext.add_agent_wallet(
    sqlc.arg(agent_profile_id)::uuid,
    sqlc.arg(operator)::payment_operator,
    sqlc.arg(msisdn),
    sqlc.arg(accepts_usd),
    sqlc.arg(accepts_cdf)
);

-- name: ListAgentWallets :many
SELECT id, agent_profile_fk, operator, msisdn, accepts_usd, accepts_cdf, created_at, updated_at
FROM payments_ext.agent_wallets
WHERE agent_profile_fk = sqlc.arg(agent_profile_id)::uuid
ORDER BY created_at;

-- name: GetAgentWalletByPair :one
-- The payment-details lookup: the wallet the client pays after a match —
-- the operator's wallet, provided it accepts the transfer's currency.
SELECT id, agent_profile_fk, operator, msisdn, accepts_usd, accepts_cdf, created_at, updated_at
FROM payments_ext.agent_wallets
WHERE agent_profile_fk = sqlc.arg(agent_profile_id)::uuid
  AND operator = sqlc.arg(operator)::payment_operator
  AND ((sqlc.arg(currency)::currency = 'usd' AND accepts_usd)
    OR (sqlc.arg(currency)::currency = 'cdf' AND accepts_cdf));

-- name: DeleteAgentWallet :exec
SELECT payments_ext.delete_agent_wallet(
    sqlc.arg(id)::uuid,
    sqlc.arg(agent_profile_id)::uuid
);
