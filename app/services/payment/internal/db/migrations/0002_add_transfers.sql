-- +migrate Up
-- Two currencies, three operators (v1). Currency is a property of the
-- WALLET, not the operator (dollarized market) — an agent declares one
-- wallet per {operator, currency} pair.
CREATE TYPE currency         AS ENUM ('usd', 'cdf');
CREATE TYPE payment_operator AS ENUM ('orange_money', 'm_pesa', 'airtel_money');

CREATE TYPE transfer_status AS ENUM
    ('pending_match', 'pending_agent_approval', 'pending_client_transfer',
     'pending_agent_transfer', 'pending_client_confirmation',
     'done', 'cancelled', 'expired', 'disputed');  -- disputed reserved, unreachable v1

CREATE TYPE offer_status AS ENUM ('offered', 'accepted', 'declined', 'voided', 'expired');

-- Saved recipients per client (D7). The transfer stores its own snapshot:
-- editing a beneficiary never rewrites history.
CREATE TABLE beneficiaries (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    client_nk  UUID NOT NULL,           -- user-service account
    name       TEXT NOT NULL,
    phone      TEXT NOT NULL,
    operator   payment_operator NOT NULL,
    city       TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ
);

CREATE UNIQUE INDEX beneficiaries_client_phone_uq
    ON beneficiaries (client_nk, phone) WHERE deleted_at IS NULL;
CREATE INDEX beneficiaries_client_idx
    ON beneficiaries (client_nk, created_at DESC) WHERE deleted_at IS NULL;

CREATE TRIGGER beneficiaries_set_updated_at
    BEFORE UPDATE ON beneficiaries
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- The agent's declared payout/receive channels. msisdn is what the client
-- pays to off-platform after a match — a wallet is more than a capability
-- flag.
CREATE TABLE agent_wallets (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_profile_fk UUID NOT NULL REFERENCES agent_profiles (id),
    operator         payment_operator NOT NULL,
    currency         currency NOT NULL,
    msisdn           TEXT NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at       TIMESTAMPTZ
);

-- One live wallet per {operator, currency} pair.
CREATE UNIQUE INDEX agent_wallets_pair_uq
    ON agent_wallets (agent_profile_fk, operator, currency) WHERE deleted_at IS NULL;

CREATE TRIGGER agent_wallets_set_updated_at
    BEFORE UPDATE ON agent_wallets
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE SEQUENCE transfer_ref_seq;

-- The transfer. Every money column is stamped at create (rate locked
-- once) and never recomputed. hold_amount is USD minor units whatever the
-- corridor — the reservation currency of the guarantee ledger.
CREATE TABLE transfers (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    reference          TEXT NOT NULL UNIQUE
        DEFAULT 'MKP-' || to_char(now(), 'YYYY') || '-' ||
                lpad(nextval('transfer_ref_seq')::text, 6, '0'),
    client_nk          UUID NOT NULL,             -- user-service account
    beneficiary_fk     UUID REFERENCES beneficiaries (id),
    -- recipient snapshot (immutable copy at create)
    recipient_name     TEXT NOT NULL,
    recipient_phone    TEXT NOT NULL,
    recipient_operator payment_operator NOT NULL,
    recipient_city     TEXT,
    -- money
    from_currency      currency NOT NULL,
    from_operator      payment_operator NOT NULL,
    from_amount        BIGINT NOT NULL,
    to_currency        currency NOT NULL,
    to_amount          BIGINT NOT NULL,
    fee_pct            NUMERIC(5,2) NOT NULL,
    fee_amount         BIGINT NOT NULL,           -- from_currency minor units
    margin_amount      BIGINT NOT NULL DEFAULT 0, -- to_currency; 0 on same-currency
    total_amount       BIGINT NOT NULL,           -- from_amount + fee_amount (what the client pays)
    rate               NUMERIC(18,6),             -- FX applied to the client; NULL = same currency
    hold_amount        BIGINT NOT NULL,           -- USD minor units, reserved at accept
    note               TEXT,                      -- client → agent
    status             transfer_status NOT NULL DEFAULT 'pending_match',
    agent_profile_fk   UUID REFERENCES agent_profiles (id),  -- set on accept
    match_round        INT NOT NULL DEFAULT 0,
    proof_client_nk    UUID,                      -- file-service NK: client-paid proof
    proof_agent_nk     UUID,                      -- file-service NK: payout proof
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at         TIMESTAMPTZ,

    CONSTRAINT transfers_amounts_ck CHECK
        (from_amount > 0 AND to_amount > 0 AND fee_amount >= 0
         AND total_amount = from_amount + fee_amount AND hold_amount > 0),
    -- CDF -> USD is not a corridor (settled 2026-07-25).
    CONSTRAINT transfers_corridor_ck CHECK
        (NOT (from_currency = 'cdf' AND to_currency = 'usd')),
    -- FX iff the currencies differ.
    CONSTRAINT transfers_rate_ck CHECK
        ((from_currency = to_currency) = (rate IS NULL))
);

CREATE INDEX transfers_client_idx ON transfers (client_nk, created_at DESC);
CREATE INDEX transfers_agent_idx  ON transfers (agent_profile_fk, created_at DESC)
    WHERE agent_profile_fk IS NOT NULL;
-- The matching queue + the rescue sweep.
CREATE INDEX transfers_queue_idx  ON transfers (status, created_at)
    WHERE status IN ('pending_match', 'pending_agent_approval');

CREATE TRIGGER transfers_set_updated_at
    BEFORE UPDATE ON transfers
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Append-only timeline (C13): every transition writes a row inside the
-- same statement — the timeline can never disagree with the status.
-- from_status is NULL only on the birth row. No updated_at/deleted_at.
CREATE TABLE transfer_events (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    transfer_fk UUID NOT NULL REFERENCES transfers (id),
    actor_nk    UUID,                    -- NULL = system/timer
    from_status transfer_status,
    to_status   transfer_status NOT NULL,
    reason      TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX transfer_events_timeline_idx ON transfer_events (transfer_fk, created_at);

-- Broadcast offers: the whole batch goes out at once, the first accept
-- wins and voids the siblings. No hold at offer time — reservation
-- happens at accept.
CREATE TABLE offers (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    transfer_fk      UUID NOT NULL REFERENCES transfers (id),
    agent_profile_fk UUID NOT NULL REFERENCES agent_profiles (id),
    match_round      INT NOT NULL,
    score            INT NOT NULL,
    status           offer_status NOT NULL DEFAULT 'offered',
    expires_at       TIMESTAMPTZ NOT NULL,
    decided_at       TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One live offer per agent per transfer (re-offers in later rounds are
-- new rows once the old one expired).
CREATE UNIQUE INDEX offers_live_uq ON offers (transfer_fk, agent_profile_fk)
    WHERE status = 'offered';
CREATE INDEX offers_agent_inbox_idx ON offers (agent_profile_fk, status, expires_at);
CREATE INDEX offers_transfer_idx ON offers (transfer_fk);

CREATE TRIGGER offers_set_updated_at
    BEFORE UPDATE ON offers
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +migrate Down
DROP TABLE offers;
DROP TABLE transfer_events;
DROP TABLE transfers;
DROP SEQUENCE transfer_ref_seq;
DROP TABLE agent_wallets;
DROP TABLE beneficiaries;
DROP TYPE offer_status;
DROP TYPE transfer_status;
DROP TYPE payment_operator;
DROP TYPE currency;
