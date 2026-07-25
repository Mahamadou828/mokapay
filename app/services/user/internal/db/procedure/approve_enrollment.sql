-- Admin approves a TOS-accepted enrollment; the caller flips the role to
-- agent in the same transaction. Only from accepted — approving a bare
-- invitation is impossible by construction.
CREATE OR REPLACE FUNCTION users_ext.approve_enrollment(
    p_id         UUID,
    p_decided_by UUID
) RETURNS SETOF enrollment_requests
LANGUAGE sql AS $$
    UPDATE enrollment_requests SET
        status     = 'approved',
        decided_by = p_decided_by,
        decided_at = now()
    WHERE id = p_id
      AND status = 'accepted'
      AND deleted_at IS NULL
    RETURNING *;
$$;
