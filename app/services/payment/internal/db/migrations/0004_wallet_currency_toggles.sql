-- +migrate Up
-- One wallet per operator with per-currency acceptance toggles, replacing
-- one row per {operator, currency}. DRC mobile-money accounts hold both
-- currencies on one MSISDN — two rows for the same phone modeled accounts
-- that don't exist — but the opt-out ("Orange, no dollars") stays, as
-- explicit flags (business decision pending on removing it entirely).
ALTER TABLE agent_wallets
    ADD COLUMN accepts_usd BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN accepts_cdf BOOLEAN NOT NULL DEFAULT TRUE;

-- Each existing row accepted exactly its stored currency.
UPDATE agent_wallets SET
    accepts_usd = (currency = 'usd'),
    accepts_cdf = (currency = 'cdf');

-- Merge live duplicates per (profile, operator): the union of the flags
-- lands on the newest row (id as tiebreak — same-transaction inserts
-- share created_at), which keeps its msisdn; the rest are soft-deleted.
UPDATE agent_wallets w SET
    accepts_usd = m.usd,
    accepts_cdf = m.cdf
FROM (
    SELECT agent_profile_fk, operator,
           bool_or(accepts_usd) AS usd,
           bool_or(accepts_cdf) AS cdf
    FROM agent_wallets
    WHERE deleted_at IS NULL
    GROUP BY agent_profile_fk, operator
) m
WHERE w.agent_profile_fk = m.agent_profile_fk
  AND w.operator = m.operator
  AND w.deleted_at IS NULL;

UPDATE agent_wallets w SET deleted_at = now()
WHERE w.deleted_at IS NULL
  AND EXISTS (SELECT 1 FROM agent_wallets n
              WHERE n.agent_profile_fk = w.agent_profile_fk
                AND n.operator = w.operator
                AND n.deleted_at IS NULL
                AND (n.created_at > w.created_at
                     OR (n.created_at = w.created_at AND n.id > w.id)));

DROP INDEX agent_wallets_pair_uq;
ALTER TABLE agent_wallets DROP COLUMN currency;
ALTER TABLE agent_wallets
    ADD CONSTRAINT agent_wallets_accepts_ck CHECK (accepts_usd OR accepts_cdf);
CREATE UNIQUE INDEX agent_wallets_operator_uq
    ON agent_wallets (agent_profile_fk, operator) WHERE deleted_at IS NULL;

-- +migrate Down
-- Lossy on purpose: a both-currencies wallet folds back to its USD row.
DROP INDEX agent_wallets_operator_uq;
ALTER TABLE agent_wallets DROP CONSTRAINT agent_wallets_accepts_ck;
ALTER TABLE agent_wallets ADD COLUMN currency currency;
UPDATE agent_wallets SET currency = CASE WHEN accepts_usd THEN 'usd' ELSE 'cdf' END::currency;
ALTER TABLE agent_wallets ALTER COLUMN currency SET NOT NULL;
ALTER TABLE agent_wallets DROP COLUMN accepts_usd, DROP COLUMN accepts_cdf;
CREATE UNIQUE INDEX agent_wallets_pair_uq
    ON agent_wallets (agent_profile_fk, operator, currency) WHERE deleted_at IS NULL;
