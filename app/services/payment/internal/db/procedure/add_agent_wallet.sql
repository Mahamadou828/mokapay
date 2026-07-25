-- Agent self-service. 23505 on the live pair index = the agent already
-- declared this {operator, currency}; update the msisdn by re-adding
-- after a remove (v1 keeps the surface minimal).
CREATE OR REPLACE FUNCTION payments_ext.add_agent_wallet(
    p_agent    UUID,
    p_operator payment_operator,
    p_currency currency,
    p_msisdn   TEXT
) RETURNS agent_wallets
LANGUAGE sql AS $$
    INSERT INTO agent_wallets (agent_profile_fk, operator, currency, msisdn)
    VALUES (p_agent, p_operator, p_currency, p_msisdn)
    RETURNING *;
$$;
