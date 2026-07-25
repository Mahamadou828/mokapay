-- THE race. Every guard lives in one gating CTE (tinfo) that also locks
-- the transfer and profile rows: offer still live, transfer still up for
-- grabs, profile active with enough available. All effects — reservation
-- (held += hold_amount), offer accepted, transfer assigned, sibling
-- offers voided, timeline row — are driven FROM tinfo, so a failed guard
-- means zero rows and ZERO side effects (an offer is never burned by a
-- failed accept). Two racing accepts serialize on the transfer row lock;
-- the loser re-evaluates the status guard against the winner's version
-- and comes back empty.
--
-- Zero rows = lost the race, offer expired/withdrawn, or insufficient
-- available — the caller reads the offer back to tell the agent which.
CREATE OR REPLACE FUNCTION payments_ext.accept_offer(
    p_offer UUID,
    p_agent UUID,
    p_actor UUID
) RETURNS SETOF transfers
LANGUAGE sql AS $$
    WITH tinfo AS (
        SELECT t.id AS transfer_id, t.hold_amount, o.id AS offer_id
        FROM offers o
        JOIN transfers t ON t.id = o.transfer_fk
        JOIN agent_profiles ap ON ap.id = o.agent_profile_fk
        WHERE o.id = p_offer
          AND o.agent_profile_fk = p_agent
          AND o.status = 'offered'
          AND o.expires_at > now()
          AND t.status = 'pending_agent_approval'
          AND ap.status = 'active'
          AND ap.deleted_at IS NULL
          AND ap.balance - ap.held >= t.hold_amount
        FOR UPDATE OF t, ap
    ), prof AS (
        UPDATE agent_profiles ap SET held = ap.held + tinfo.hold_amount
        FROM tinfo
        WHERE ap.id = p_agent
        RETURNING ap.id
    ), won AS (
        UPDATE offers o SET status = 'accepted', decided_at = now()
        FROM tinfo
        WHERE o.id = tinfo.offer_id
    ), t AS (
        UPDATE transfers tr SET status = 'pending_client_transfer',
            agent_profile_fk = p_agent
        FROM tinfo
        WHERE tr.id = tinfo.transfer_id
        RETURNING tr.*
    ), voided AS (
        UPDATE offers o SET status = 'voided', decided_at = now()
        FROM tinfo
        WHERE o.transfer_fk = tinfo.transfer_id
          AND o.status = 'offered'
          AND o.id <> tinfo.offer_id
    ), ev AS (
        INSERT INTO transfer_events (transfer_fk, actor_nk, from_status, to_status)
        SELECT tinfo.transfer_id, p_actor,
               'pending_agent_approval', 'pending_client_transfer'
        FROM tinfo
    )
    SELECT * FROM t;
$$;
