-- Live notifications; the inbox query orders and filters on top.
CREATE OR REPLACE VIEW users_ext.notifications AS
SELECT id,
       account_fk,
       event_type,
       title,
       body,
       data,
       status,
       read_at,
       created_at,
       updated_at
FROM notifications
WHERE deleted_at IS NULL;
