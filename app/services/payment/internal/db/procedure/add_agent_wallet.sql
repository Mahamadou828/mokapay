-- Agent self-service. 23505 on the live operator index = the agent
-- already declared this operator; update the account number or flags by
-- re-adding after a remove (v1 keeps the surface minimal). At least one
-- currency flag must be set (table CHECK backs the api-level guard).
CREATE OR REPLACE FUNCTION payments_ext.add_agent_wallet(
    p_agent          UUID,
    p_operator       payment_operator,
    p_account_number TEXT,
    p_accepts_usd    BOOLEAN,
    p_accepts_cdf    BOOLEAN
) RETURNS agent_wallets
LANGUAGE sql AS $$
    INSERT INTO agent_wallets (agent_profile_fk, operator, account_number, accepts_usd, accepts_cdf)
    VALUES (p_agent, p_operator, p_account_number, p_accepts_usd, p_accepts_cdf)
    RETURNING *;
$$;
