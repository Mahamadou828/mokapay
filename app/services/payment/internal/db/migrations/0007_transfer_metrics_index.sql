-- +migrate Up
-- The AML metrics snapshot (payment → compliance) aggregates a recipient's
-- inbound transfers by phone on every completion. The sender and agent
-- sides already have their (id, created_at DESC) indexes; the recipient
-- side had none — this is that index, and nothing else in the service
-- reads transfers by phone yet.
CREATE INDEX transfers_recipient_phone_idx
    ON transfers (recipient_phone, created_at DESC);

-- +migrate Down
DROP INDEX transfers_recipient_phone_idx;
