-- Live profiles. available is derived here and ONLY here — never stored.
CREATE OR REPLACE VIEW payments_ext.agent_profiles AS
SELECT id,
       account_nk,
       status,
       level,
       currency,
       balance,
       held,
       (balance - held)::BIGINT AS available,
       guarantee_floor,
       city,
       created_at,
       updated_at
FROM agent_profiles
WHERE deleted_at IS NULL;
