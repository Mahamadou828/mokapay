-- The weekly payout: link every unpaid accrual of one agent's accounting
-- week to a batch row, itself backed by the `commission` ledger request
-- the caller created in the SAME transaction. Linking and totalling are
-- one statement, so a batch can never claim rows it did not total.
--
-- p_total is the amount the caller already put on the ledger request: the
-- guard `agg.total = p_total` makes a concurrently accrued row (which
-- would silently change the sum) abort the whole statement instead of
-- paying a number nobody authorized. Zero rows also covers the re-run
-- case — commission_batches_week_uq means one payout per agent per week,
-- whatever re-triggers the job.
CREATE OR REPLACE FUNCTION payments_ext.add_commission_batch(
    p_agent           UUID,
    p_week            DATE,
    p_total           BIGINT,
    p_ledger_request  UUID
) RETURNS SETOF commission_batches
LANGUAGE sql AS $$
    WITH due AS (
        SELECT id, payable_amount
        FROM commissions
        WHERE agent_profile_fk = p_agent
          AND accounting_week = p_week
          AND batch_fk IS NULL
        FOR UPDATE
    ), agg AS (
        SELECT count(*) AS n, COALESCE(sum(payable_amount), 0) AS total FROM due
    ), b AS (
        INSERT INTO commission_batches
            (agent_profile_fk, accounting_week, commission_count, total_amount, ledger_request_fk)
        SELECT p_agent, p_week, agg.n, agg.total, p_ledger_request
        FROM agg
        WHERE agg.n > 0 AND agg.total = p_total
        ON CONFLICT (agent_profile_fk, accounting_week) DO NOTHING
        RETURNING *
    ), link AS (
        UPDATE commissions c SET batch_fk = b.id
        FROM b
        WHERE c.id IN (SELECT id FROM due)
    )
    SELECT * FROM b;
$$;
