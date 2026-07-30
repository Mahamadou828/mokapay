-- The accrual, written inside the SAME transaction as the transfer's move
-- to a terminal that earns commission. Every amount is already computed by
-- the commission domain (integer math, unit-tested) — SQL stores, it does
-- not decide.
--
-- Zero rows = this transfer already has its accrual (ON CONFLICT DO
-- NOTHING on transfer_fk). That is the whole idempotency story: replaying
-- a completion, or resolving a dispute on an already-accrued transfer,
-- cannot double-book. No backfill script can ever be needed to repair it.
CREATE OR REPLACE FUNCTION payments_ext.add_commission(
    p_transfer              UUID,
    p_agent                 UUID,
    p_fee_currency          currency,
    p_fee_amount            BIGINT,
    p_agent_amount          BIGINT,
    p_platform_amount       BIGINT,
    p_guarantee_fund_amount BIGINT,
    p_margin_currency       currency,
    p_margin_amount         BIGINT,
    p_payable_amount        BIGINT,
    p_accounting_week       DATE
) RETURNS SETOF commissions
LANGUAGE sql AS $$
    INSERT INTO commissions (
        transfer_fk, agent_profile_fk,
        fee_currency, fee_amount,
        agent_amount, platform_amount, guarantee_fund_amount,
        margin_currency, margin_amount,
        payable_amount, accounting_week
    )
    VALUES (
        p_transfer, p_agent,
        p_fee_currency, p_fee_amount,
        p_agent_amount, p_platform_amount, p_guarantee_fund_amount,
        p_margin_currency, p_margin_amount,
        p_payable_amount, p_accounting_week
    )
    ON CONFLICT (transfer_fk) DO NOTHING
    RETURNING *;
$$;
