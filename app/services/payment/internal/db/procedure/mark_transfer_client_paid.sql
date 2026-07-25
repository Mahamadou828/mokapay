-- Proof move #1 (system-only at the RPC layer): the client paid the
-- agent; the sender proof lands and the ball moves to the agent. Forward-
-- only by the status guard — a redelivered proof event is zero rows,
-- idempotent for the worker.
CREATE OR REPLACE FUNCTION payments_ext.mark_transfer_client_paid(
    p_transfer UUID,
    p_proof    UUID,
    p_actor    UUID DEFAULT NULL
) RETURNS SETOF transfers
LANGUAGE sql AS $$
    WITH t AS (
        UPDATE transfers SET status = 'pending_agent_transfer',
            proof_client_nk = p_proof
        WHERE id = p_transfer AND status = 'pending_client_transfer'
        RETURNING *
    ), ev AS (
        INSERT INTO transfer_events (transfer_fk, actor_nk, from_status, to_status, reason)
        SELECT t.id, p_actor, 'pending_client_transfer', 'pending_agent_transfer',
               'client payment proof received'
        FROM t
    )
    SELECT * FROM t;
$$;
