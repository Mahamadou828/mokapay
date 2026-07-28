-- The ONLY write path into the audit log. Appends the evaluation and, when
-- it triggered, opens its paired alert in the same statement: an
-- evaluation and the alert it justifies are one fact, so they commit
-- together or not at all. The alert CTE is data-modifying, so Postgres
-- runs it whether or not the outer SELECT references it.
--
-- Parameter order differs from a plain column order: every optional
-- parameter carries DEFAULT NULL and those must come last.
CREATE OR REPLACE FUNCTION compliance_ext.add_rule_evaluation(
    p_transfer_nk     UUID,
    p_rule_code       compliance_rule_code,
    p_subject_type    compliance_subject_type,
    p_triggered       BOOLEAN,
    p_inputs          JSONB,
    p_account_nk      UUID                DEFAULT NULL,
    p_recipient_phone TEXT                DEFAULT NULL,
    p_severity        compliance_severity DEFAULT NULL,
    p_risk_points     INT                 DEFAULT 0
) RETURNS rule_evaluations
LANGUAGE sql AS $$
    WITH ev AS (
        INSERT INTO rule_evaluations
            (transfer_nk, rule_code, subject_type, account_nk, recipient_phone,
             triggered, severity, risk_points, inputs)
        VALUES
            (p_transfer_nk, p_rule_code, p_subject_type, p_account_nk, p_recipient_phone,
             p_triggered, p_severity, COALESCE(p_risk_points, 0), p_inputs)
        RETURNING *
    ), alert AS (
        INSERT INTO alerts (rule_evaluation_fk)
        SELECT ev.id FROM ev WHERE p_triggered
        RETURNING id
    )
    SELECT * FROM ev;
$$;
