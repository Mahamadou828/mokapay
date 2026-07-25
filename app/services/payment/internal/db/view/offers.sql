-- Raw offers (engine reads).
CREATE OR REPLACE VIEW payments_ext.offers AS
SELECT id,
       transfer_fk,
       agent_profile_fk,
       match_round,
       score,
       status,
       expires_at,
       decided_at,
       created_at,
       updated_at
FROM offers;
