-- A decline is offer-level (no transfer transition): the agent is out of
-- this transfer for good — the matching pre-filter excludes declined
-- agents on every later round. Zero rows = not the agent's live offer.
CREATE OR REPLACE FUNCTION payments_ext.decline_offer(
    p_offer UUID,
    p_agent UUID
) RETURNS SETOF offers
LANGUAGE sql AS $$
    UPDATE offers SET status = 'declined', decided_at = now()
    WHERE id = p_offer AND agent_profile_fk = p_agent AND status = 'offered'
    RETURNING *;
$$;
