-- Live agent wallets: the {operator, currency} pairs an agent can serve,
-- with the msisdn the client pays to after a match.
CREATE OR REPLACE VIEW payments_ext.agent_wallets AS
SELECT id,
       agent_profile_fk,
       operator,
       currency,
       msisdn,
       created_at,
       updated_at
FROM agent_wallets
WHERE deleted_at IS NULL;
