-- The dispute read model, carrying the transfer identity the ops queue
-- needs (reference, both parties) so the list never fans out into
-- per-row transfer reads.
-- Disputes have no soft delete (workflow history) — the view exists for
-- read-model consistency: every read goes through payments_ext.
CREATE OR REPLACE VIEW payments_ext.disputes AS
SELECT d.id,
       d.transfer_fk,
       t.reference AS transfer_reference,
       t.client_nk,
       t.agent_profile_fk,
       d.opened_by_party,
       d.opened_by,
       d.reason,
       d.details,
       d.from_status,
       d.hold_amount,
       d.refund_currency,
       d.refund_amount,
       d.status,
       d.resolution,
       d.hold_outcome,
       d.resolution_note,
       d.deduction_fk,
       d.resolved_by,
       d.resolved_at,
       d.created_at,
       d.updated_at
FROM disputes d
JOIN transfers t ON t.id = d.transfer_fk;
