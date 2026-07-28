-- Live agent wallets: one per operator, with the msisdn the client pays
-- to after a match and the currencies the agent accepts on it.
CREATE OR REPLACE VIEW payments_ext.agent_wallets AS
SELECT id,
       agent_profile_fk,
       operator,
       msisdn,
       accepts_usd,
       accepts_cdf,
       created_at,
       updated_at
FROM agent_wallets
WHERE deleted_at IS NULL;
