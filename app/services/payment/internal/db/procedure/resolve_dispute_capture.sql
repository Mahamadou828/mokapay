-- The agent-at-fault ruling: THE hold -> balance crossover. The guarantee
-- is captured (held -= X AND balance -= X) and the crossover goes through
-- a ledger request exactly like every other balance movement — created
-- already settled, with its immutable ledger line and balance_after, in
-- the same statement. The request is inlined here rather than routed
-- through add_ledger_request because that procedure only accepts OPEN
-- statuses on purpose (creation must never bypass settlement); a capture
-- is decided and settled in the same breath, like approve_ledger_request.
--
-- The client's refund is funded FROM this capture but is paid
-- off-platform: ledger requests are agent-scoped and a client has no
-- account here, so a second (outflow) request would simply debit the
-- agent twice. The amount owed is stamped on the dispute
-- (refund_amount/refund_currency) as the ops instruction, and the usecase
-- raises the D10 shortfall alert when the capture cannot cover it.
--
-- balance >= held >= hold_amount holds by construction (the CHECK on
-- agent_profiles and the reservation placed at accept), so the capture
-- can never drive the balance negative.
--
-- Zero rows = unknown dispute, already resolved, or the transfer left
-- `disputed` meanwhile.
CREATE OR REPLACE FUNCTION payments_ext.resolve_dispute_capture(
    p_dispute     UUID,
    p_resolved_by UUID,
    p_note        TEXT DEFAULT NULL
) RETURNS SETOF disputes
LANGUAGE sql AS $$
    WITH dinfo AS (
        SELECT d.id, d.transfer_fk, t.hold_amount, t.agent_profile_fk, t.reference
        FROM disputes d
        JOIN transfers t ON t.id = d.transfer_fk
        WHERE d.id = p_dispute
          AND d.status = 'open'
          AND t.status = 'disputed'
          AND t.agent_profile_fk IS NOT NULL
        FOR UPDATE OF d, t
    ), req AS (
        INSERT INTO ledger_requests
            (ref, agent_profile_fk, type, direction, amount, status,
             reference_entity_type, reference_entity_id, reason,
             created_by, decided_by, decided_at)
        SELECT 'DED-' || to_char(now(), 'YYYY') || '-' ||
                   lpad(nextval('ledger_ref_seq')::text, 6, '0'),
               dinfo.agent_profile_fk, 'deduction', 'outflow', dinfo.hold_amount, 'done',
               'transfer', dinfo.transfer_fk,
               'guarantee captured: dispute on transfer ' || dinfo.reference,
               p_resolved_by, p_resolved_by, now()
        FROM dinfo
        RETURNING id, agent_profile_fk, amount
    ), prof AS (
        UPDATE agent_profiles ap SET
            balance = ap.balance - req.amount,
            held    = ap.held - req.amount
        FROM req
        WHERE ap.id = req.agent_profile_fk
        RETURNING ap.id, ap.balance
    ), line AS (
        INSERT INTO ledger_lines (agent_profile_fk, ledger_request_fk, amount, balance_after)
        SELECT prof.id, req.id, -req.amount, prof.balance
        FROM req, prof
    ), t AS (
        UPDATE transfers tr SET status = 'refunded'
        FROM dinfo
        WHERE tr.id = dinfo.transfer_fk
    ), ev AS (
        INSERT INTO transfer_events (transfer_fk, actor_nk, from_status, to_status, reason)
        SELECT dinfo.transfer_fk, p_resolved_by, 'disputed', 'refunded', p_note
        FROM dinfo
    )
    UPDATE disputes d SET
        status          = 'resolved',
        resolution      = 'agent_fault',
        hold_outcome    = 'captured',
        resolution_note = p_note,
        deduction_fk    = (SELECT id FROM req),
        resolved_by     = p_resolved_by,
        resolved_at     = now()
    FROM dinfo
    WHERE d.id = dinfo.id
    RETURNING d.*;
$$;
