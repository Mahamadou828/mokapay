-- The ext schema holds every view and procedure; dropped and re-created on
-- each Migrate() run so definitions never drift.
DROP SCHEMA IF EXISTS users_ext CASCADE;
CREATE SCHEMA users_ext;
