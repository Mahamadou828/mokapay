-- The payment-timeout worker's exit: the client never paid within the
-- window. Only from pending_client_transfer — where the hold is always
-- placed — so the release rides unconditionally. Zero rows = the state
-- moved on (proof arrived first); the job just completes.
CREATE OR REPLACE FUNCTION payments_ext.cancel_transfer_timeout(
    p_transfer UUID,
    p_reason   TEXT
) RETURNS SETOF transfers
LANGUAGE sql AS $$
    WITH tinfo AS (
        SELECT id, hold_amount, agent_profile_fk
        FROM transfers
        WHERE id = p_transfer AND status = 'pending_client_transfer'
        FOR UPDATE
    ), rel AS (
        UPDATE agent_profiles ap SET held = ap.held - tinfo.hold_amount
        FROM tinfo
        WHERE ap.id = tinfo.agent_profile_fk
    ), t AS (
        UPDATE transfers tr SET status = 'cancelled'
        FROM tinfo
        WHERE tr.id = tinfo.id
        RETURNING tr.*
    ), ev AS (
        INSERT INTO transfer_events (transfer_fk, from_status, to_status, reason)
        SELECT tinfo.id, 'pending_client_transfer', 'cancelled', p_reason FROM tinfo
    )
    SELECT * FROM t;
$$;
