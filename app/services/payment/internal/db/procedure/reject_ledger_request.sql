-- Terminal reject from any non-terminal state; an outflow's reservation
-- is released in the same statement (the prof CTE runs exactly once even
-- though the primary query only reads req).
CREATE OR REPLACE FUNCTION payments_ext.reject_ledger_request(
    p_id         UUID,
    p_decided_by UUID,
    p_reason     TEXT DEFAULT NULL
) RETURNS SETOF ledger_requests
LANGUAGE sql AS $$
    WITH req AS (
        UPDATE ledger_requests SET
            status     = 'rejected',
            reason     = COALESCE(p_reason, reason),
            decided_by = p_decided_by,
            decided_at = now()
        WHERE id = p_id
          AND status IN ('initiated', 'pending_agent_action', 'pending_admin_approval')
        RETURNING *
    ), prof AS (
        UPDATE agent_profiles ap SET
            held = ap.held - CASE WHEN req.direction = 'outflow' THEN req.amount ELSE 0 END
        FROM req
        WHERE ap.id = req.agent_profile_fk
        RETURNING ap.id
    )
    SELECT req.* FROM req;
$$;
