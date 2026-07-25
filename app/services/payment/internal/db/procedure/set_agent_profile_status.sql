-- The one procedure under the intent RPCs (Activate/Suspend); transition
-- legality (pending→active, active→suspended, …) is the usecase's guard.
CREATE OR REPLACE FUNCTION payments_ext.set_agent_profile_status(
    p_id     UUID,
    p_status agent_profile_status
) RETURNS SETOF agent_profiles
LANGUAGE sql AS $$
    UPDATE agent_profiles SET status = p_status
    WHERE id = p_id AND deleted_at IS NULL
    RETURNING *;
$$;
