-- +migrate Up
-- A wallet on `rawbank`/`equity_bcdc` (0008) holds a bank account number,
-- not a phone number, so the column name stops being true. Straight
-- rename: nothing references it by name except the view, the procedure
-- and the queries, all of which are re-created on every Migrate().
ALTER TABLE agent_wallets RENAME COLUMN msisdn TO account_number;

-- +migrate Down
ALTER TABLE agent_wallets RENAME COLUMN account_number TO msisdn;
