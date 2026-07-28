-- Partial update: NULL keeps the current value. Moving to a closed_*
-- status stamps closed_at once — a case that is re-closed keeps the
-- timestamp of the first closure, and re-opening clears it.
-- SETOF so an unknown or soft-deleted id yields zero rows.
CREATE OR REPLACE FUNCTION compliance_ext.update_case(
    p_id          UUID,
    p_status      compliance_case_status DEFAULT NULL,
    p_assignee_nk UUID                   DEFAULT NULL,
    p_summary     TEXT                   DEFAULT NULL
) RETURNS SETOF cases
LANGUAGE sql AS $$
    UPDATE cases SET
        status      = COALESCE(p_status,      status),
        assignee_nk = COALESCE(p_assignee_nk, assignee_nk),
        summary     = COALESCE(p_summary,     summary),
        closed_at   = CASE
            WHEN COALESCE(p_status, status) IN ('closed_sar', 'closed_no_action')
                THEN COALESCE(closed_at, now())
            ELSE NULL
        END
    WHERE id = p_id AND deleted_at IS NULL
    RETURNING *;
$$;
