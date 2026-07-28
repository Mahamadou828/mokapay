-- The accrual read model. status is DERIVED here, never stored: unbatched
-- is `accrued`, batched is `paid` once the batch's ledger request settled,
-- `batched` while it waits. One fact, one place.
-- Accruals have no soft delete (immutable bookkeeping) — the view exists
-- for read-model consistency: every read goes through payments_ext.
CREATE OR REPLACE VIEW payments_ext.commissions AS
SELECT c.id,
       c.transfer_fk,
       t.reference AS transfer_reference,
       c.agent_profile_fk,
       c.fee_currency,
       c.fee_amount,
       c.agent_amount,
       c.platform_amount,
       c.guarantee_fund_amount,
       c.margin_currency,
       c.margin_amount,
       c.payable_amount,
       c.accounting_week,
       c.batch_fk,
       (CASE
            WHEN c.batch_fk IS NULL THEN 'accrued'
            WHEN lr.status = 'done' THEN 'paid'
            ELSE 'batched'
        END)::commission_status AS status,
       c.created_at,
       c.updated_at
FROM commissions c
JOIN transfers t ON t.id = c.transfer_fk
LEFT JOIN commission_batches b ON b.id = c.batch_fk
LEFT JOIN ledger_requests lr ON lr.id = b.ledger_request_fk;
