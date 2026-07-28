-- name: ListMatchCandidates :many
-- The pre-filter (one query, per the design): hard gates only — active,
-- can cover the reservation, holds BOTH wallet ends, not the client
-- themselves, hasn't declined this transfer. Scoring happens in Go over
-- this shortlist; ORDER BY pre-ranks so the LIMIT keeps the strongest.
SELECT ap.id,
       ap.account_nk,
       ap.level,
       ap.city,
       (ap.balance - ap.held)::BIGINT AS available,
       s.done_count,
       s.decided_count,
       s.active_load,
       s.declined_count
FROM agent_profiles ap
JOIN payments_ext.agent_stats s ON s.agent_profile_fk = ap.id
WHERE ap.status = 'active'
  AND ap.deleted_at IS NULL
  AND ap.balance - ap.held >= sqlc.arg(hold_amount)
  AND ap.account_nk <> sqlc.arg(client_nk)::uuid
  AND EXISTS (SELECT 1 FROM agent_wallets w
              WHERE w.agent_profile_fk = ap.id AND w.deleted_at IS NULL
                AND w.operator = sqlc.arg(from_operator)::payment_operator
                AND ((sqlc.arg(from_currency)::currency = 'usd' AND w.accepts_usd)
                  OR (sqlc.arg(from_currency)::currency = 'cdf' AND w.accepts_cdf)))
  AND EXISTS (SELECT 1 FROM agent_wallets w
              WHERE w.agent_profile_fk = ap.id AND w.deleted_at IS NULL
                AND w.operator = sqlc.arg(to_operator)::payment_operator
                AND ((sqlc.arg(to_currency)::currency = 'usd' AND w.accepts_usd)
                  OR (sqlc.arg(to_currency)::currency = 'cdf' AND w.accepts_cdf)))
  AND NOT EXISTS (SELECT 1 FROM offers o
                  WHERE o.transfer_fk = sqlc.arg(transfer_id)::uuid
                    AND o.agent_profile_fk = ap.id
                    AND o.status = 'declined')
ORDER BY ap.level DESC, available DESC
LIMIT sqlc.arg(max_rows);
