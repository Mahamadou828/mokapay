-- Create with reservation (design L1): an outflow reserves its amount on
-- the spot — held += amount behind the feasibility guards, in the same
-- statement that inserts the request. The profile UPDATE takes the row
-- lock, so two racing creates serialize and the second re-evaluates the
-- guards against the new held: over-promising the same money is
-- impossible.
--
-- Zero rows = profile unknown/deleted, outflow on a non-active profile,
-- or insufficient available funds (incl. the withdrawal guarantee floor).
-- A 23505 on ledger_requests_idempotency_uq = producer retry; the whole
-- statement (reservation included) rolls back — the caller re-reads by
-- key. Inflows skip every funds guard and are allowed on pending profiles
-- (the enrollment guarantee funds a not-yet-active agent).
CREATE OR REPLACE FUNCTION payments_ext.add_ledger_request(
    p_agent           UUID,
    p_type            ledger_request_type,
    p_direction       ledger_direction,
    p_amount          BIGINT,
    p_idempotency_key TEXT,
    p_enrollment_nk   UUID DEFAULT NULL,
    p_reason          TEXT DEFAULT NULL
) RETURNS SETOF ledger_requests
LANGUAGE sql AS $$
    WITH prof AS (
        UPDATE agent_profiles SET
            held = held + CASE WHEN p_direction = 'outflow' THEN p_amount ELSE 0 END
        WHERE id = p_agent
          AND deleted_at IS NULL
          AND (p_direction = 'inflow' OR status = 'active')
          AND (p_direction = 'inflow'
               OR balance - held - p_amount >=
                  CASE WHEN p_type = 'withdrawal' THEN guarantee_floor ELSE 0 END)
        RETURNING id
    )
    INSERT INTO ledger_requests
        (agent_profile_fk, type, direction, amount, idempotency_key, enrollment_nk, reason)
    SELECT prof.id, p_type, p_direction, p_amount, p_idempotency_key, p_enrollment_nk, p_reason
    FROM prof
    RETURNING *;
$$;
