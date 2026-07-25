-- Broadcasts one offer round: transfer -> pending_agent_approval, the
-- whole batch inserted at once, timeline row — one statement. Zero rows =
-- the transfer left pending_match meanwhile (raced with a cancel/expire);
-- the caller just drops the round.
CREATE OR REPLACE FUNCTION payments_ext.offer_transfer(
    p_transfer   UUID,
    p_agents     UUID[],
    p_scores     INT[],
    p_expires_at TIMESTAMPTZ
) RETURNS SETOF offers
LANGUAGE sql AS $$
    WITH t AS (
        UPDATE transfers SET status = 'pending_agent_approval'
        WHERE id = p_transfer AND status = 'pending_match'
        RETURNING id, match_round
    ), ins AS (
        INSERT INTO offers (transfer_fk, agent_profile_fk, match_round, score, expires_at)
        SELECT t.id, cand.agent, t.match_round, cand.score, p_expires_at
        FROM t, unnest(p_agents, p_scores) AS cand(agent, score)
        RETURNING *
    ), ev AS (
        INSERT INTO transfer_events (transfer_fk, from_status, to_status, reason)
        SELECT t.id, 'pending_match', 'pending_agent_approval',
               'offered to ' || cardinality(p_agents) || ' agent(s)'
        FROM t
    )
    SELECT * FROM ins;
$$;
