-- Live cases.
CREATE OR REPLACE VIEW compliance_ext.cases AS
SELECT id,
       reference,
       status,
       assignee_nk,
       summary,
       closed_at,
       created_at,
       updated_at
FROM cases
WHERE deleted_at IS NULL;
