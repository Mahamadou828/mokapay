-- Live transfers. Nothing deletes a transfer today (workflow history) —
-- the filter is read-model consistency with the rest of payments_ext.
CREATE OR REPLACE VIEW payments_ext.transfers AS
SELECT id,
       reference,
       client_nk,
       beneficiary_fk,
       recipient_name,
       recipient_phone,
       recipient_operator,
       recipient_city,
       from_currency,
       from_operator,
       from_amount,
       to_currency,
       to_amount,
       fee_pct,
       fee_amount,
       margin_amount,
       total_amount,
       rate,
       hold_amount,
       note,
       status,
       agent_profile_fk,
       match_round,
       proof_client_nk,
       proof_agent_nk,
       created_at,
       updated_at
FROM transfers
WHERE deleted_at IS NULL;
