-- Candidate declines the invitation. Only from invited — after accepting
-- the terms, the exit is an admin cancel. The candidate is their own
-- decided_by.
CREATE OR REPLACE FUNCTION users_ext.decline_enrollment(
    p_id      UUID,
    p_account UUID
) RETURNS SETOF enrollment_requests
LANGUAGE sql AS $$
    UPDATE enrollment_requests SET
        status     = 'declined',
        decided_by = p_account,
        decided_at = now()
    WHERE id = p_id
      AND account_fk = p_account
      AND status = 'invited'
      AND deleted_at IS NULL
    RETURNING *;
$$;
