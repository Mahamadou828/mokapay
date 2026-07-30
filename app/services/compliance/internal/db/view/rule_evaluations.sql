-- The immutable audit log. No deleted_at filter: the table has none —
-- an evaluation is never hidden.
CREATE OR REPLACE VIEW compliance_ext.rule_evaluations AS
SELECT id,
       transfer_nk,
       rule_code,
       subject_type,
       account_nk,
       recipient_phone,
       triggered,
       severity,
       risk_points,
       inputs,
       evaluated_at
FROM rule_evaluations;
