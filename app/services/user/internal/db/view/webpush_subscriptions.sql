-- Live push registrations — the delivery routes of an account's devices.
CREATE OR REPLACE VIEW users_ext.webpush_subscriptions AS
SELECT id,
       account_fk,
       endpoint,
       p256dh,
       auth,
       user_agent,
       created_at,
       updated_at
FROM webpush_subscriptions
WHERE deleted_at IS NULL;
