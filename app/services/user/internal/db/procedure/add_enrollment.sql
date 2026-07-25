-- The invite. A live enrollment already existing surfaces as a 23505 on
-- enrollment_requests_live_uq. Account eligibility (active, role user) is
-- the usecase's check — this procedure owns only its own table.
CREATE OR REPLACE FUNCTION users_ext.add_enrollment(
    p_account    UUID,
    p_invited_by UUID
) RETURNS enrollment_requests
LANGUAGE sql AS $$
    INSERT INTO enrollment_requests (account_fk, invited_by)
    VALUES (p_account, p_invited_by)
    RETURNING *;
$$;
