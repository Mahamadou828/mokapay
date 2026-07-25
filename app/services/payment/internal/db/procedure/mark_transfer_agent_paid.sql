-- Proof move #2 (system-only at the RPC layer): the agent paid the
-- recipient out; the payout proof lands and the client gets the
-- confirmation window. A proof can never reach 'done' — only the
-- client's confirm (or the auto-confirm timer) releases money.
CREATE OR REPLACE FUNCTION payments_ext.mark_transfer_agent_paid(
    p_transfer UUID,
    p_proof    UUID,
    p_actor    UUID DEFAULT NULL
) RETURNS SETOF transfers
LANGUAGE sql AS $$
    WITH t AS (
        UPDATE transfers SET status = 'pending_client_confirmation',
            proof_agent_nk = p_proof
        WHERE id = p_transfer AND status = 'pending_agent_transfer'
        RETURNING *
    ), ev AS (
        INSERT INTO transfer_events (transfer_fk, actor_nk, from_status, to_status, reason)
        SELECT t.id, p_actor, 'pending_agent_transfer', 'pending_client_confirmation',
               'agent payout proof received'
        FROM t
    )
    SELECT * FROM t;
$$;
