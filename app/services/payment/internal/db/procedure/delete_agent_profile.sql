-- Soft delete, only for a zero-money profile — never delete a balance or
-- an open reservation. Zero rows = unknown id, already deleted, or funds
-- present (the caller disambiguates by reading the profile).
CREATE OR REPLACE FUNCTION payments_ext.delete_agent_profile(
    p_id UUID
) RETURNS SETOF agent_profiles
LANGUAGE sql AS $$
    UPDATE agent_profiles SET deleted_at = now()
    WHERE id = p_id
      AND deleted_at IS NULL
      AND balance = 0
      AND held = 0
    RETURNING *;
$$;
