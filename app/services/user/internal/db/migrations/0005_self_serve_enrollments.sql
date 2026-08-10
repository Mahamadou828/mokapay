-- +migrate Up
-- Self-serve enrollment (AG-03/AG-04): the candidate signs themselves up,
-- so there is no inviter. NULL invited_by IS the marker — no second table
-- and no extra flag column, which keeps enrollment_requests_live_uq
-- covering both entry points with the one partial unique index it already
-- has ("no more than one live enrollment per account").
ALTER TABLE enrollment_requests ALTER COLUMN invited_by DROP NOT NULL;

COMMENT ON COLUMN enrollment_requests.invited_by IS
    'ops/admin who invited the candidate; NULL = self-serve enrollment';

-- +migrate Down
-- Only reversible while no self-serve row exists; that is the honest
-- rollback, since restoring NOT NULL would otherwise have to invent an
-- inviter.
ALTER TABLE enrollment_requests ALTER COLUMN invited_by SET NOT NULL;
