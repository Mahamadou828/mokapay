-- Creates the transfer with every money column stamped (rate locked once,
-- never recomputed) and writes the birth timeline row in the same
-- statement. The corridor/rate/amount invariants are CHECK constraints —
-- a bad payload aborts, it never half-lands.
CREATE OR REPLACE FUNCTION payments_ext.add_transfer(
    p_client             UUID,
    p_beneficiary        UUID,
    p_recipient_name     TEXT,
    p_recipient_phone    TEXT,
    p_recipient_operator payment_operator,
    p_from_currency      currency,
    p_from_operator      payment_operator,
    p_from_amount        BIGINT,
    p_to_currency        currency,
    p_to_amount          BIGINT,
    p_fee_pct            NUMERIC,
    p_fee_amount         BIGINT,
    p_margin_amount      BIGINT,
    p_hold_amount        BIGINT,
    p_rate               NUMERIC DEFAULT NULL,
    p_recipient_city     TEXT DEFAULT NULL,
    p_note               TEXT DEFAULT NULL
) RETURNS SETOF transfers
LANGUAGE sql AS $$
    WITH t AS (
        INSERT INTO transfers
            (client_nk, beneficiary_fk, recipient_name, recipient_phone,
             recipient_operator, recipient_city, from_currency, from_operator,
             from_amount, to_currency, to_amount, fee_pct, fee_amount,
             margin_amount, total_amount, rate, hold_amount, note)
        VALUES
            (p_client, p_beneficiary, p_recipient_name, p_recipient_phone,
             p_recipient_operator, p_recipient_city, p_from_currency, p_from_operator,
             p_from_amount, p_to_currency, p_to_amount, p_fee_pct, p_fee_amount,
             p_margin_amount, p_from_amount + p_fee_amount, p_rate, p_hold_amount, p_note)
        RETURNING *
    ), ev AS (
        INSERT INTO transfer_events (transfer_fk, actor_nk, from_status, to_status)
        SELECT t.id, p_client, NULL, 'pending_match' FROM t
    )
    SELECT * FROM t;
$$;
