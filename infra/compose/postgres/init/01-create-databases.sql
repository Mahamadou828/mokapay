-- Per-service databases. Each Go service owns its own database (objects live in
-- that database's public schema). Runs only on first cluster init — i.e. when the
-- postgres_data volume is empty. Add one line per new service, e.g.:
--   CREATE DATABASE example;
--
-- To (re)create these on an existing volume, either recreate the DB manually
-- (createdb -U "$POSTGRES_USER" example) or reset the volume:
--   docker compose down -v && docker compose up -d

CREATE DATABASE users;
CREATE DATABASE files;
CREATE DATABASE payments;
CREATE DATABASE compliance;
