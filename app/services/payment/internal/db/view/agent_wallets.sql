-- Live agent wallets: one per operator, with the account number the
-- client pays to after a match (an MSISDN on a mobile-money operator, a
-- bank account number on a bank one) and the currencies the agent accepts
-- on it.
CREATE OR REPLACE VIEW payments_ext.agent_wallets AS
SELECT id,
       agent_profile_fk,
       operator,
       account_number,
       accepts_usd,
       accepts_cdf,
       created_at,
       updated_at
FROM agent_wallets
WHERE deleted_at IS NULL;
