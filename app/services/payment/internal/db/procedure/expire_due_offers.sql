-- Expires the transfer's overdue live offers (offer-level; the transfer
-- transition, if any, is requeue_unmatched_transfer's job). Returns the
-- expired rows so the worker can log the round.
CREATE OR REPLACE FUNCTION payments_ext.expire_due_offers(
    p_transfer UUID
) RETURNS SETOF offers
LANGUAGE sql AS $$
    UPDATE offers SET status = 'expired', decided_at = now()
    WHERE transfer_fk = p_transfer AND status = 'offered' AND expires_at <= now()
    RETURNING *;
$$;
