-- Candidate accepts the terms. Guarded on state AND ownership; SETOF so a
-- wrong id/owner/state yields zero rows (pgx.ErrNoRows) — accept only
-- moves invited → accepted.
CREATE OR REPLACE FUNCTION users_ext.accept_enrollment(
    p_id          UUID,
    p_account     UUID,
    p_tos_version TEXT
) RETURNS SETOF enrollment_requests
LANGUAGE sql AS $$
    UPDATE enrollment_requests SET
        status          = 'accepted',
        tos_version     = p_tos_version,
        tos_accepted_at = now()
    WHERE id = p_id
      AND account_fk = p_account
      AND status = 'invited'
      AND deleted_at IS NULL
    RETURNING *;
$$;
