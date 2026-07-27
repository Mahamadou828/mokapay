-- Ledger requests have no soft delete (workflow history) — the view exists
-- for read-model consistency: every read goes through payments_ext.
CREATE OR REPLACE VIEW payments_ext.ledger_requests AS
SELECT id,
       ref,
       agent_profile_fk,
       type,
       direction,
       amount,
       status,
       reference_entity_type,
       reference_entity_id,
       proof_file_nk,
       reason,
       created_by,
       decided_by,
       decided_at,
       created_at,
       updated_at
FROM ledger_requests;
