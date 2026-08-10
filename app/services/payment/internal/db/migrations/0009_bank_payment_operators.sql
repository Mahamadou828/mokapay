-- +migrate Up
-- Bank channels ship at launch (design v2, slice 6): the catalogue an
-- agent declares a wallet on — and that the client picks "vous payez via"
-- from — is no longer mobile money only. `afrimoney` is a fourth
-- mobile-money operator; `rawbank` and `equity_bcdc` are banks, so the
-- account number behind them is an RIB rather than an MSISDN (hence the
-- msisdn -> account_number rename in 0010).
--
-- Its own migration file on purpose: an enum value added inside a
-- transaction cannot be USED by that same transaction, so anything
-- referencing the new labels must land in a later one (SQL rules,
-- "Enum strategy") — same reasoning as 0005.
ALTER TYPE payment_operator ADD VALUE IF NOT EXISTS 'afrimoney';
ALTER TYPE payment_operator ADD VALUE IF NOT EXISTS 'rawbank';
ALTER TYPE payment_operator ADD VALUE IF NOT EXISTS 'equity_bcdc';

-- +migrate Down
-- Postgres cannot drop an enum value; the labels stay in the type.
SELECT 1;
