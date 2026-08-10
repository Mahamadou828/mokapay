-- +migrate Up
-- The launch send ladder: five more source currencies alongside usd, all
-- one-way into cdf (eur, zar, aoa, zmw, tzs). The client picks what they
-- fund with; the recipient is always paid in francs, so no reverse
-- direction and no new destination is introduced here.
--
-- Its own migration file on purpose: an enum value added inside a
-- transaction cannot be USED by that same transaction, so anything that
-- references these labels must land in a later one (SQL rules, "Enum
-- strategy"). The corridor gate stays in the usecase (validateCorridor) —
-- the 0002 CHECK only ever forbade the one inverted pair.
ALTER TYPE currency ADD VALUE IF NOT EXISTS 'eur';
ALTER TYPE currency ADD VALUE IF NOT EXISTS 'zar';
ALTER TYPE currency ADD VALUE IF NOT EXISTS 'aoa';
ALTER TYPE currency ADD VALUE IF NOT EXISTS 'zmw';
ALTER TYPE currency ADD VALUE IF NOT EXISTS 'tzs';

-- +migrate Down
-- Postgres cannot drop an enum value; the labels stay in the type. Rolling
-- back the rows that carry them is the meaningful undo.
SELECT 1;
