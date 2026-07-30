-- Three of the four rulings end the same way for the money: the hold is
-- released and no balance moves (agent_right and client_fraud make the
-- agent whole, no_fault unwinds a transfer nobody funded). Only the
-- terminal status differs, and the caller passes it — the resolution
-- table lives in the dispute domain (unit-tested), SQL applies it.
--
-- Release and terminal move are one statement behind a double status
-- guard (dispute still open AND transfer still disputed), so a double
-- resolve leaves exactly one winner and exactly one release.
--
-- Zero rows = unknown dispute, already resolved, or the transfer left
-- `disputed` meanwhile.
CREATE OR REPLACE FUNCTION payments_ext.resolve_dispute_release(
    p_dispute     UUID,
    p_resolution  dispute_resolution,
    p_terminal    transfer_status,
    p_resolved_by UUID,
    p_note        TEXT DEFAULT NULL
) RETURNS SETOF disputes
LANGUAGE sql AS $$
    WITH dinfo AS (
        SELECT d.id, d.transfer_fk, t.hold_amount, t.agent_profile_fk
        FROM disputes d
        JOIN transfers t ON t.id = d.transfer_fk
        WHERE d.id = p_dispute
          AND d.status = 'open'
          AND t.status = 'disputed'
        FOR UPDATE OF d, t
    ), rel AS (
        UPDATE agent_profiles ap SET held = ap.held - dinfo.hold_amount
        FROM dinfo
        WHERE dinfo.agent_profile_fk IS NOT NULL AND ap.id = dinfo.agent_profile_fk
    ), t AS (
        UPDATE transfers tr SET status = p_terminal
        FROM dinfo
        WHERE tr.id = dinfo.transfer_fk
    ), ev AS (
        INSERT INTO transfer_events (transfer_fk, actor_nk, from_status, to_status, reason)
        SELECT dinfo.transfer_fk, p_resolved_by, 'disputed', p_terminal, p_note
        FROM dinfo
    )
    UPDATE disputes d SET
        status          = 'resolved',
        resolution      = p_resolution,
        hold_outcome    = 'released',
        resolution_note = p_note,
        resolved_by     = p_resolved_by,
        resolved_at     = now()
    FROM dinfo
    WHERE d.id = dinfo.id
    RETURNING d.*;
$$;
