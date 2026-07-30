-- Links an alert to a case. Idempotent: the pair IS the primary key, so a
-- repeated link conflicts, does nothing, and the UNION re-selects the
-- existing row — the caller always gets the link back, never an error.
-- A foreign case or alert id still fails on the FK, which is the point.
CREATE OR REPLACE FUNCTION compliance_ext.add_case_alert(
    p_case_id  UUID,
    p_alert_id UUID
) RETURNS SETOF case_alerts
LANGUAGE sql AS $$
    WITH ins AS (
        INSERT INTO case_alerts (case_fk, alert_fk)
        VALUES (p_case_id, p_alert_id)
        ON CONFLICT DO NOTHING
        RETURNING *
    )
    SELECT * FROM ins
    UNION ALL
    SELECT ca.* FROM case_alerts ca
    WHERE ca.case_fk = p_case_id
      AND ca.alert_fk = p_alert_id
      AND NOT EXISTS (SELECT 1 FROM ins);
$$;
