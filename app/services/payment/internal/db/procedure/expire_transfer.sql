-- Terminal expiry: match rounds/deadline exhausted. Only reachable from
-- the two pre-accept states, so there is never a hold to release; live
-- offers are voided. The gating CTE locks the row and captures the
-- from-status for the timeline.
CREATE OR REPLACE FUNCTION payments_ext.expire_transfer(
    p_transfer UUID,
    p_reason   TEXT
) RETURNS SETOF transfers
LANGUAGE sql AS $$
    WITH tinfo AS (
        SELECT id, status AS from_status
        FROM transfers
        WHERE id = p_transfer
          AND status IN ('pending_match', 'pending_agent_approval')
        FOR UPDATE
    ), t AS (
        UPDATE transfers tr SET status = 'expired'
        FROM tinfo
        WHERE tr.id = tinfo.id
        RETURNING tr.*
    ), voided AS (
        UPDATE offers o SET status = 'voided', decided_at = now()
        FROM tinfo
        WHERE o.transfer_fk = tinfo.id AND o.status = 'offered'
    ), ev AS (
        INSERT INTO transfer_events (transfer_fk, from_status, to_status, reason)
        SELECT tinfo.id, tinfo.from_status, 'expired', p_reason FROM tinfo
    )
    SELECT * FROM t;
$$;
