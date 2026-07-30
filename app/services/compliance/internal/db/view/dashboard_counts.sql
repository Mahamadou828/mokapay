-- One row, all the counts the admin compliance page needs. Severity
-- counts cover the UNRESOLVED alerts only (an alert that was reviewed and
-- closed is not a backlog item).
CREATE OR REPLACE VIEW compliance_ext.dashboard_counts AS
SELECT COUNT(*) FILTER (WHERE a.status = 'open')::BIGINT         AS open_alerts,
       COUNT(*) FILTER (WHERE a.status = 'under_review')::BIGINT AS under_review_alerts,
       COUNT(*) FILTER (WHERE a.status = 'escalated')::BIGINT    AS escalated_alerts,
       COUNT(*) FILTER (WHERE re.severity = 'high'
                          AND a.status NOT IN ('resolved', 'dismissed'))::BIGINT   AS high_severity_alerts,
       COUNT(*) FILTER (WHERE re.severity = 'medium'
                          AND a.status NOT IN ('resolved', 'dismissed'))::BIGINT   AS medium_severity_alerts,
       COUNT(*) FILTER (WHERE re.severity = 'low'
                          AND a.status NOT IN ('resolved', 'dismissed'))::BIGINT   AS low_severity_alerts,
       -- Distinct transfers carrying at least one unresolved alert.
       COUNT(DISTINCT re.transfer_nk)
           FILTER (WHERE a.status NOT IN ('resolved', 'dismissed'))::BIGINT        AS flagged_transfers,
       (SELECT COUNT(*) FROM cases c
        WHERE c.deleted_at IS NULL AND c.status = 'open')::BIGINT                  AS open_cases,
       (SELECT COUNT(*) FROM cases c
        WHERE c.deleted_at IS NULL AND c.status = 'investigating')::BIGINT         AS investigating_cases,
       (SELECT COUNT(*) FROM cases c
        WHERE c.deleted_at IS NULL AND c.status = 'closed_sar')::BIGINT            AS sar_cases
FROM alerts a
JOIN rule_evaluations re ON re.id = a.rule_evaluation_fk
WHERE a.deleted_at IS NULL;
