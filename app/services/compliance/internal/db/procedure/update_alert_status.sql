-- Partial update of the review lifecycle: NULL keeps the current value.
-- SETOF so an unknown (or soft-deleted) id yields zero rows — "not found"
-- — instead of a NULL composite.
CREATE OR REPLACE FUNCTION compliance_ext.update_alert_status(
    p_id              UUID,
    p_status          compliance_alert_status,
    p_assignee_nk     UUID DEFAULT NULL,
    p_resolution_note TEXT DEFAULT NULL
) RETURNS SETOF alerts
LANGUAGE sql AS $$
    UPDATE alerts SET
        status          = COALESCE(p_status,          status),
        assignee_nk     = COALESCE(p_assignee_nk,     assignee_nk),
        resolution_note = COALESCE(p_resolution_note, resolution_note)
    WHERE id = p_id AND deleted_at IS NULL
    RETURNING *;
$$;
