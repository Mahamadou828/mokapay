-- Forward-only between the pending states — this is the proof-reporting
-- path (file service) and it must never be able to move money or close a
-- request. Attaches the proof when given; a repeated delivery of the same
-- event yields zero rows (already forward), which callers treat as done.
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
      )
    RETURNING *;
$$;
