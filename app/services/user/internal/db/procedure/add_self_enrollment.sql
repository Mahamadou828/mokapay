-- The self-serve sign-up (AG-04). No inviter, and the terms are stamped
-- here rather than by a later accept_enrollment call: the candidate ticked
-- the box on the very form that created this row, so the request is born
-- 'accepted' — the state that already means "terms stamped, guarantee
-- funding open, waiting on the money". A live enrollment already existing
-- surfaces as a 23505 on enrollment_requests_live_uq, exactly as it does
-- for an invite. Account eligibility is the usecase's check.
CREATE OR REPLACE FUNCTION users_ext.add_self_enrollment(
    p_account     UUID,
    p_tos_version TEXT
) RETURNS enrollment_requests
LANGUAGE sql AS $$
    INSERT INTO enrollment_requests (account_fk, invited_by, status, tos_version, tos_accepted_at)
    VALUES (p_account, NULL, 'accepted', p_tos_version, now())
    RETURNING *;
$$;
