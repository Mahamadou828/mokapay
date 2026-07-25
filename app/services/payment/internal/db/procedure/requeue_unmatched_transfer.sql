-- The round is over and nobody accepted: back to the matching queue,
-- round++. Guarded on no live offer remaining — a racing accept keeps the
-- transfer (zero rows here). No hold to release: reservation only exists
-- from accept on.
CREATE OR REPLACE FUNCTION payments_ext.requeue_unmatched_transfer(
    p_transfer UUID
) RETURNS SETOF transfers
LANGUAGE sql AS $$
    WITH t AS (
        UPDATE transfers SET status = 'pending_match', match_round = match_round + 1
        WHERE id = p_transfer
          AND status = 'pending_agent_approval'
          AND NOT EXISTS (
              SELECT 1 FROM offers o
              WHERE o.transfer_fk = p_transfer AND o.status = 'offered')
        RETURNING *
    ), ev AS (
        INSERT INTO transfer_events (transfer_fk, from_status, to_status, reason)
        SELECT t.id, 'pending_agent_approval', 'pending_match', 'offer round exhausted'
        FROM t
    )
    SELECT * FROM t;
$$;
