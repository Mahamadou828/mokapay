-- The only path to done: the client's confirm or the auto-confirm timer
-- (p_actor NULL). Releases the hold in the same statement; the status
-- guard makes the release exactly-once — a double confirm or a
-- confirm-vs-timer race leaves exactly one winner and one release.
CREATE OR REPLACE FUNCTION payments_ext.complete_transfer(
    p_transfer UUID,
    p_actor    UUID DEFAULT NULL,
    p_reason   TEXT DEFAULT NULL
) RETURNS SETOF transfers
LANGUAGE sql AS $$
    WITH tinfo AS (
        SELECT id, hold_amount, agent_profile_fk
        FROM transfers
        WHERE id = p_transfer AND status = 'pending_client_confirmation'
        FOR UPDATE
    ), rel AS (
        UPDATE agent_profiles ap SET held = ap.held - tinfo.hold_amount
        FROM tinfo
        WHERE ap.id = tinfo.agent_profile_fk
    ), t AS (
        UPDATE transfers tr SET status = 'done'
        FROM tinfo
        WHERE tr.id = tinfo.id
        RETURNING tr.*
    ), ev AS (
        INSERT INTO transfer_events (transfer_fk, actor_nk, from_status, to_status, reason)
        SELECT tinfo.id, p_actor, 'pending_client_confirmation', 'done', p_reason
        FROM tinfo
    )
    SELECT * FROM t;
$$;
