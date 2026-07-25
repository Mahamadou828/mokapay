-- The C13 timeline, append-only.
CREATE OR REPLACE VIEW payments_ext.transfer_events AS
SELECT id,
       transfer_fk,
       actor_nk,
       from_status,
       to_status,
       reason,
       created_at
FROM transfer_events;
