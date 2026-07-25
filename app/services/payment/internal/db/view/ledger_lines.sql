-- The immutable statement.
CREATE OR REPLACE VIEW payments_ext.ledger_lines AS
SELECT id,
       agent_profile_fk,
       ledger_request_fk,
       amount,
       balance_after,
       created_at
FROM ledger_lines;
