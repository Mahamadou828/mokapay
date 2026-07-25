-- Moves between the OPEN states only — this path must never be able to
-- move money or close a request. Forward: initiated → either pending,
-- pending_agent_action → pending_admin_approval (proof reported).
-- Backward: pending_admin_approval → pending_agent_action (the admin
-- sends it back for a better proof). Attaches the proof when given; a
-- repeated delivery of the same event yields zero rows, which callers
-- treat as done.
CREATE OR REPLACE FUNCTION payments_ext.update_ledger_request_status(
    p_id         UUID,
    p_status     ledger_request_status,
    p_proof_file UUID DEFAULT NULL
) RETURNS SETOF ledger_requests
LANGUAGE sql AS $$
    UPDATE ledger_requests SET
        status        = p_status,
        proof_file_nk = COALESCE(p_proof_file, proof_file_nk)
    WHERE id = p_id
      AND (
            (status = 'initiated'
             AND p_status IN ('pending_agent_action', 'pending_admin_approval'))
         OR (status = 'pending_agent_action'
             AND p_status = 'pending_admin_approval')
         OR (status = 'pending_admin_approval'
             AND p_status = 'pending_agent_action')
      )
    RETURNING *;
$$;
