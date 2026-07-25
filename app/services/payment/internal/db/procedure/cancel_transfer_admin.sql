-- The ops escape hatch: cancel from any OPEN state. A post-accept
-- transfer always carries a placed hold and a set agent (accept sets
-- both atomically; terminals release), so agent_profile_fk IS NOT NULL is
-- exactly the release condition — and the status guard makes the release
-- exactly-once.
CREATE OR REPLACE FUNCTION payments_ext.cancel_transfer_admin(
    p_transfer UUID,
    p_actor    UUID,
    p_reason   TEXT
) RETURNS SETOF transfers
LANGUAGE sql AS $$
    WITH tinfo AS (
        SELECT id, status AS from_status, hold_amount, agent_profile_fk
        FROM transfers
        WHERE id = p_transfer
          AND status IN ('pending_match', 'pending_agent_approval',
                         'pending_client_transfer', 'pending_agent_transfer',
                         'pending_client_confirmation')
        FOR UPDATE
    ), rel AS (
        UPDATE agent_profiles ap SET held = ap.held - tinfo.hold_amount
        FROM tinfo
        WHERE tinfo.agent_profile_fk IS NOT NULL AND ap.id = tinfo.agent_profile_fk
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
        SELECT tinfo.id, p_actor, tinfo.from_status, 'cancelled', p_reason FROM tinfo
    )
    SELECT * FROM t;
$$;
