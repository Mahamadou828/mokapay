-- The agent inbox: an offer with the money summary the agent decides on.
-- Only projects the offer's FK to the transfer (view ID rule).
CREATE OR REPLACE VIEW payments_ext.agent_offers AS
SELECT o.id,
       o.transfer_fk,
       o.agent_profile_fk,
       o.match_round,
       o.score,
       o.status,
       o.expires_at,
       o.decided_at,
       o.created_at,
       t.reference          AS transfer_reference,
       t.from_currency,
       t.from_operator,
       t.from_amount,
       t.to_currency,
       t.recipient_operator,
       t.recipient_city,
       t.to_amount,
       t.total_amount,
       t.hold_amount,
       t.note
FROM offers o
JOIN transfers t ON t.id = o.transfer_fk;
