-- Ledger requests have no soft delete (workflow history) — the view exists
-- for read-model consistency: every read goes through payments_ext.
CREATE OR REPLACE VIEW payments_ext.ledger_requests AS
SELECT id,
       agent_profile_fk,
       type,
       direction,
       amount,
       status,
       enrollment_nk,
       proof_file_nk,
       reason,
       idempotency_key,
       decided_by,
       decided_at,
       created_at,
       updated_at
FROM ledger_requests;
