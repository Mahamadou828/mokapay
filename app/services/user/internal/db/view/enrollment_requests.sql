-- Live enrollment requests, terminal and in-flight alike (history is part
-- of the read model — GetMyEnrollment returns the latest row whatever its
-- status).
CREATE OR REPLACE VIEW users_ext.enrollment_requests AS
SELECT id,
       account_fk,
       invited_by,
       status,
       tos_version,
       tos_accepted_at,
       decided_by,
       decided_at,
       created_at,
       updated_at
FROM enrollment_requests
WHERE deleted_at IS NULL;
