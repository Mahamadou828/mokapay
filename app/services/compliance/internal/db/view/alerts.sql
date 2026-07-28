-- The review worklist. The rule/subject columns come from the joined
-- evaluation so the list renders without a second read; only the FK is
-- projected from the join (never re.id — it is the same value).
CREATE OR REPLACE VIEW compliance_ext.alerts AS
SELECT a.id,
       a.rule_evaluation_fk,
       a.status,
       a.assignee_nk,
       a.resolution_note,
       re.rule_code,
       re.subject_type,
       re.severity,
       re.transfer_nk,
       re.account_nk,
       re.recipient_phone,
       re.risk_points,
       re.evaluated_at,
       a.created_at,
       a.updated_at
FROM alerts a
JOIN rule_evaluations re ON re.id = a.rule_evaluation_fk
WHERE a.deleted_at IS NULL;
