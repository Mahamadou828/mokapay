-- The settlement — the only statement in the system that changes balance.
-- One CTE chain: close the request (status guard: settles exactly once,
-- racing approves lose on zero rows) → move the money (the UPDATE takes
-- the profile row lock; an outflow consumes the reservation placed at
-- create: balance and held decrease together) → write the immutable line
-- with balance_after. The CHECK constraints and the UNIQUE line FK are
-- the last-resort guards; by construction they never fire.
CREATE OR REPLACE FUNCTION payments_ext.approve_ledger_request(
    p_id         UUID,
    p_decided_by UUID
) RETURNS SETOF ledger_requests
LANGUAGE sql AS $$
    WITH req AS (
        UPDATE ledger_requests SET
            status     = 'done',
            decided_by = p_decided_by,
            decided_at = now()
        WHERE id = p_id
          AND status = 'pending_admin_approval'
        RETURNING *
    ), prof AS (
        UPDATE agent_profiles ap SET
            balance = ap.balance +
                CASE req.direction WHEN 'inflow' THEN req.amount ELSE -req.amount END,
            held = ap.held -
                CASE req.direction WHEN 'outflow' THEN req.amount ELSE 0 END
        FROM req
        WHERE ap.id = req.agent_profile_fk
        RETURNING ap.id, ap.balance
    ), line AS (
        INSERT INTO ledger_lines (agent_profile_fk, ledger_request_fk, amount, balance_after)
        SELECT prof.id, req.id,
               CASE req.direction WHEN 'inflow' THEN req.amount ELSE -req.amount END,
               prof.balance
        FROM req, prof
        RETURNING id
    )
    SELECT req.* FROM req;
$$;
