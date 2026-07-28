-- The weekly payout read model. `paid` follows the batch's ledger request
-- — the settlement is the single source of truth, so the batch can never
-- claim to be paid while the money has not moved.
CREATE OR REPLACE VIEW payments_ext.commission_batches AS
SELECT b.id,
       b.agent_profile_fk,
       b.accounting_week,
       b.commission_count,
       b.total_amount,
       b.ledger_request_fk,
       lr.ref AS ledger_request_ref,
       lr.status AS ledger_request_status,
       (CASE WHEN lr.status = 'done' THEN 'paid' ELSE 'batched' END)::commission_status AS status,
       b.created_at,
       b.updated_at
FROM commission_batches b
JOIN ledger_requests lr ON lr.id = b.ledger_request_fk;
