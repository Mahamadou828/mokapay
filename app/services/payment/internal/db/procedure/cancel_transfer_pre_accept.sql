-- The client's own cancel: only before an agent committed (no hold yet).
-- Post-accept exits are the timeout worker's or an admin's.
CREATE OR REPLACE FUNCTION payments_ext.cancel_transfer_pre_accept(
    p_transfer UUID,
    p_client   UUID,
    p_reason   TEXT DEFAULT NULL
) RETURNS SETOF transfers
LANGUAGE sql AS $$
    WITH tinfo AS (
        SELECT id, status AS from_status
        FROM transfers
        WHERE id = p_transfer
          AND client_nk = p_client
          AND status IN ('pending_match', 'pending_agent_approval')
        FOR UPDATE
    ), t AS (
        UPDATE transfers tr SET status = 'cancelled'
        FROM tinfo
        WHERE tr.id = tinfo.id
        RETURNING tr.*
    ), voided AS (
        UPDATE offers o SET status = 'voided', decided_at = now()
        FROM tinfo
        WHERE o.transfer_fk = tinfo.id AND o.status = 'offered'
    ), ev AS (
        INSERT INTO transfer_events (transfer_fk, actor_nk, from_status, to_status, reason)
        SELECT tinfo.id, p_client, tinfo.from_status, 'cancelled', p_reason FROM tinfo
    )
    SELECT * FROM t;
$$;
