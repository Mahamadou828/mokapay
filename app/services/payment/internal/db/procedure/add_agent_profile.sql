-- A 23505 on agent_profiles_account_uq = the account already has a live
-- profile (idempotent creators treat it as success-by-lookup).
CREATE OR REPLACE FUNCTION payments_ext.add_agent_profile(
    p_account         UUID,
    p_currency        TEXT   DEFAULT 'USD',
    p_guarantee_floor BIGINT DEFAULT 2000,
    p_city            TEXT   DEFAULT NULL
) RETURNS agent_profiles
LANGUAGE sql AS $$
    INSERT INTO agent_profiles (account_nk, currency, guarantee_floor, city)
    VALUES (p_account, p_currency, p_guarantee_floor, p_city)
    RETURNING *;
$$;
