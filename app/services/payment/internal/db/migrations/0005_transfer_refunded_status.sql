-- +migrate Up
-- The dispute path's fourth terminal (spec "Dispute / bad-transfer path"):
-- agent at fault -> guarantee captured, client refunded from it -> the
-- transfer ends `refunded`, distinct from `cancelled` (nobody was out
-- money) and `done` (the payout stands).
--
-- Its own migration file on purpose: an enum value added inside a
-- transaction cannot be USED by that same transaction, so the tables and
-- procedures that reference it must land in later ones (SQL rules,
-- "Enum strategy").
ALTER TYPE transfer_status ADD VALUE IF NOT EXISTS 'refunded';

-- +migrate Down
-- Postgres cannot drop an enum value; the label stays in the type. Rolling
-- back the tables that use it (0006) is the meaningful undo.
SELECT 1;
