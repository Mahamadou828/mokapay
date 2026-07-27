-- Create with reservation (design L1): an outflow reserves its amount on
-- the spot — held += amount behind the feasibility guards, in the same
-- statement that inserts the request. The profile UPDATE takes the row
-- lock, so two racing creates serialize and the second re-evaluates the
-- guards against the new held: over-promising the same money is
-- impossible.
--
-- Zero rows = profile unknown/deleted, outflow on a non-active profile,
-- or insufficient available funds (incl. the withdrawal guarantee floor).
-- A 23505 on ledger_requests_reference_uq = the entity already has a
-- live-or-settled request; the whole statement (reservation included)
-- rolls back — the caller re-reads by reference. Inflows skip every funds
-- guard and are allowed on pending profiles (the enrollment guarantee
-- funds a not-yet-active agent).
--
-- p_status: a request lands where its next actor waits (admin-created
-- funding -> pending_agent_action; agent withdrawal ->
-- pending_admin_approval). Only OPEN statuses are accepted — landing on
-- done/rejected/disputed would bypass the settlement statement, so the
-- guard makes it impossible (zero rows).
CREATE OR REPLACE FUNCTION payments_ext.add_ledger_request(
    p_agent           UUID,
    p_type            ledger_request_type,
    p_direction       ledger_direction,
    p_amount          BIGINT,
    p_created_by      UUID,
    p_status          ledger_request_status DEFAULT 'initiated',
    p_reference_type  ledger_reference_type DEFAULT NULL,
    p_reference_id    UUID DEFAULT NULL,
    p_reason          TEXT DEFAULT NULL
) RETURNS SETOF ledger_requests
LANGUAGE sql AS $$
    WITH prof AS (
        UPDATE agent_profiles SET
            held = held + CASE WHEN p_direction = 'outflow' THEN p_amount ELSE 0 END
        WHERE id = p_agent
          AND deleted_at IS NULL
          AND p_status IN ('initiated', 'pending_agent_action', 'pending_admin_approval')
          AND (p_direction = 'inflow' OR status = 'active')
          AND (p_direction = 'inflow'
               OR balance - held - p_amount >=
                  CASE WHEN p_type = 'withdrawal' THEN guarantee_floor ELSE 0 END)
        RETURNING id
    )
    INSERT INTO ledger_requests
        (ref, agent_profile_fk, type, direction, amount, created_by, status, reference_entity_type, reference_entity_id, reason)
    SELECT CASE p_type
               WHEN 'funding'    THEN 'DEP'
               WHEN 'withdrawal' THEN 'WDR'
               WHEN 'refund'     THEN 'RFD'
               WHEN 'deduction'  THEN 'DED'
               WHEN 'commission' THEN 'COM'
           END || '-' || to_char(now(), 'YYYY') || '-' ||
           lpad(nextval('ledger_ref_seq')::text, 6, '0'),
           prof.id, p_type, p_direction, p_amount, p_created_by, p_status, p_reference_type, p_reference_id, p_reason
    FROM prof
    RETURNING *;
$$;
