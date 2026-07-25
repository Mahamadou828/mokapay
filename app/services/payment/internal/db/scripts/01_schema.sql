-- The ext schema holds every view and procedure; dropped and re-created on
-- each Migrate() run so they always match the embedded SQL.
DROP SCHEMA IF EXISTS payments_ext CASCADE;
CREATE SCHEMA payments_ext;
