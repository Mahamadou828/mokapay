-- Freeze a contested transfer. The core rule of the whole dispute path:
-- the transfer goes `disputed` and the hold STAYS held — no profile row is
-- touched here. The collateral is what funds the fix, so it is never
-- released until an admin rules.
--
-- Only the three post-accept states can be disputed: before an agent
-- committed there is no hold, no counterparty and no money at risk — that
-- is a cancel, not a dispute. The status guard also makes a second open
-- dispute impossible (a `disputed` transfer is not in the set).
--
-- The dispute stamps its own snapshot of the transfer (where it froze,
-- what is held, what the client paid) so a later ruling reads the dispute
-- rather than a transfer that moved on.
--
-- Zero rows = unknown transfer or a state that cannot be disputed.
CREATE OR REPLACE FUNCTION payments_ext.open_dispute(
    p_transfer  UUID,
    p_party     dispute_party,
    p_opened_by UUID DEFAULT NULL,   -- NULL = system auto-dispute
    p_reason    dispute_reason DEFAULT 'other',
    p_details   TEXT DEFAULT NULL
) RETURNS SETOF disputes
LANGUAGE sql AS $$
    WITH tinfo AS (
        SELECT id, status AS from_status, hold_amount, total_amount, from_currency
        FROM transfers
        WHERE id = p_transfer
          AND status IN ('pending_client_transfer', 'pending_agent_transfer',
                         'pending_client_confirmation')
        FOR UPDATE
    ), t AS (
        UPDATE transfers tr SET status = 'disputed'
        FROM tinfo
        WHERE tr.id = tinfo.id
    ), ev AS (
        INSERT INTO transfer_events (transfer_fk, actor_nk, from_status, to_status, reason)
        SELECT tinfo.id, p_opened_by, tinfo.from_status, 'disputed', p_details
        FROM tinfo
    )
    INSERT INTO disputes
        (transfer_fk, opened_by_party, opened_by, reason, details,
         from_status, hold_amount, refund_currency, refund_amount)
    SELECT tinfo.id, p_party, p_opened_by, p_reason, p_details,
           tinfo.from_status, tinfo.hold_amount, tinfo.from_currency, tinfo.total_amount
    FROM tinfo
    RETURNING *;
$$;
