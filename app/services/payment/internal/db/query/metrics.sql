-- The AML metrics snapshot payment ships to the compliance service. Three
-- read-only aggregations over payments_ext.transfers, one per subject
-- (sender, agent, recipient). Conventions shared by all three:
--
--   * money is summed as hold_amount — USD minor units of the client's FULL
--     total whatever the corridor, so it is the one column that can be
--     summed across a mixed usd/cdf history. The caller divides by 100:
--     compliance thresholds are whole dollars.
--   * windows count from created_at — when the sender initiated, which is
--     the act the velocity and structuring rules are about (a transfer
--     confirmed days later still belongs to the day it was started).
--   * cancelled and expired transfers are excluded: nobody was ever asked
--     for money. Everything else counts, in-flight included — an attempted
--     structuring pattern is exactly what these rules look for.

-- name: GetSenderTransferMetrics :one
-- The sender's rolling aggregates, including the transfer being evaluated
-- (it is one of their last 24 hours' transfers — the daily-limit rule
-- reads the total, not the delta).
SELECT
    coalesce(sum(hold_amount) FILTER (WHERE created_at > now() - INTERVAL '24 hours'), 0)::BIGINT AS volume_24h_minor,
    coalesce(sum(hold_amount) FILTER (WHERE created_at > now() - INTERVAL '7 days'), 0)::BIGINT   AS volume_7d_minor,
    count(*) FILTER (WHERE created_at > now() - INTERVAL '7 days')::INT                           AS count_7d,
    coalesce(sum(hold_amount), 0)::BIGINT                                                         AS volume_30d_minor,
    count(*) FILTER (WHERE created_at > now() - INTERVAL '1 hour')::INT                           AS count_1h,
    count(*) FILTER (WHERE created_at > now() - INTERVAL '7 days'
        AND (extract(HOUR FROM created_at AT TIME ZONE sqlc.arg(timezone)::text) >= 23
          OR extract(HOUR FROM created_at AT TIME ZONE sqlc.arg(timezone)::text) < 5))::INT             AS count_7d_nighttime
FROM payments_ext.transfers
WHERE client_nk = sqlc.arg(client_nk)::uuid
  AND status NOT IN ('cancelled', 'expired')
  AND created_at > now() - INTERVAL '30 days';

-- name: GetAgentTransferMetrics :one
-- The same shape on the payout side, keyed by the agent profile that
-- accepted. agent_profile_fk is NULL until an agent accepts, so a
-- pre-match transfer simply contributes to nobody.
SELECT
    coalesce(sum(hold_amount) FILTER (WHERE created_at > now() - INTERVAL '24 hours'), 0)::BIGINT AS volume_24h_minor,
    coalesce(sum(hold_amount) FILTER (WHERE created_at > now() - INTERVAL '7 days'), 0)::BIGINT   AS volume_7d_minor,
    count(*) FILTER (WHERE created_at > now() - INTERVAL '7 days')::INT                           AS count_7d,
    coalesce(sum(hold_amount), 0)::BIGINT                                                         AS volume_30d_minor,
    count(*) FILTER (WHERE created_at > now() - INTERVAL '1 hour')::INT                           AS count_1h,
    count(*) FILTER (WHERE created_at > now() - INTERVAL '7 days'
        AND (extract(HOUR FROM created_at AT TIME ZONE sqlc.arg(timezone)::text) >= 23
          OR extract(HOUR FROM created_at AT TIME ZONE sqlc.arg(timezone)::text) < 5))::INT             AS count_7d_nighttime
FROM payments_ext.transfers
WHERE agent_profile_fk = sqlc.arg(agent_profile_id)::uuid
  AND status NOT IN ('cancelled', 'expired')
  AND created_at > now() - INTERVAL '30 days';

-- name: GetRecipientTransferMetrics :one
-- How many distinct senders funnelled to this recipient over the week —
-- the collection-account signal. The phone is the recipient's identity:
-- recipients are off-platform and have no account.
SELECT count(DISTINCT client_nk)::INT AS distinct_senders_7d
FROM payments_ext.transfers
WHERE recipient_phone = sqlc.arg(recipient_phone)
  AND status NOT IN ('cancelled', 'expired')
  AND created_at > now() - INTERVAL '7 days';
