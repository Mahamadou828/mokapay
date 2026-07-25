-- name: AddAgentProfile :one
-- 23505 on agent_profiles_account_uq = the account already has a live profile.
SELECT * FROM payments_ext.add_agent_profile(
    sqlc.arg(account_id)::uuid,
    sqlc.arg(currency),
    sqlc.arg(guarantee_floor),
    sqlc.narg(city)
);

-- name: GetAgentProfile :one
SELECT id, account_nk, status, level, currency, balance, held, available, guarantee_floor, city, created_at, updated_at
FROM payments_ext.agent_profiles
WHERE id = sqlc.arg(id)::uuid;

-- name: GetAgentProfileByAccount :one
SELECT id, account_nk, status, level, currency, balance, held, available, guarantee_floor, city, created_at, updated_at
FROM payments_ext.agent_profiles
WHERE account_nk = sqlc.arg(account_id)::uuid;

-- name: SetAgentProfileStatus :one
-- Zero rows (pgx.ErrNoRows) = unknown or deleted profile.
SELECT * FROM payments_ext.set_agent_profile_status(
    sqlc.arg(id)::uuid,
    sqlc.arg(status)::agent_profile_status
);

-- name: DeleteAgentProfile :one
-- Zero rows = unknown id, already deleted, or the profile still holds
-- money — the repo disambiguates by reading the profile.
SELECT * FROM payments_ext.delete_agent_profile(sqlc.arg(id)::uuid);
