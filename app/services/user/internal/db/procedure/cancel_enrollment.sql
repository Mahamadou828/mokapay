-- Admin cancels an in-flight enrollment (either live state). The caller
-- flips the candidate's role back to user in the same transaction.
CREATE OR REPLACE FUNCTION users_ext.cancel_enrollment(
    p_id         UUID,
    p_decided_by UUID
) RETURNS SETOF enrollment_requests
LANGUAGE sql AS $$
    UPDATE enrollment_requests SET
        status     = 'cancelled',
        decided_by = p_decided_by,
        decided_at = now()
    WHERE id = p_id
      AND status IN ('invited', 'accepted')
      AND deleted_at IS NULL
    RETURNING *;
$$;
