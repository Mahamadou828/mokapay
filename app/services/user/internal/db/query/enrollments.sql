-- name: AddEnrollment :one
-- 23505 on enrollment_requests_live_uq = the account already has a live
-- enrollment.
SELECT * FROM users_ext.add_enrollment(
    sqlc.arg(account_id)::uuid,
    sqlc.arg(invited_by)::uuid
);

-- name: AddSelfEnrollment :one
-- The self-serve sign-up: no inviter, terms stamped inline, born
-- 'accepted'. Same 23505 on enrollment_requests_live_uq as AddEnrollment.
SELECT * FROM users_ext.add_self_enrollment(
    sqlc.arg(account_id)::uuid,
    sqlc.arg(tos_version)
);

-- name: GetEnrollment :one
SELECT id, account_fk, invited_by, status, tos_version, tos_accepted_at, decided_by, decided_at, created_at, updated_at
FROM users_ext.enrollment_requests
WHERE id = sqlc.arg(id)::uuid;

-- name: GetLatestEnrollmentByAccount :one
-- The account's most recent request, whatever its status — what the
-- client renders (invitation banner, TOS gate, or history).
SELECT id, account_fk, invited_by, status, tos_version, tos_accepted_at, decided_by, decided_at, created_at, updated_at
FROM users_ext.enrollment_requests
WHERE account_fk = sqlc.arg(account_id)::uuid
ORDER BY created_at DESC
LIMIT 1;

-- name: ListEnrollments :many
-- The ops dashboard. The status filter is optional: NULL disables it.
SELECT id, account_fk, invited_by, status, tos_version, tos_accepted_at, decided_by, decided_at, created_at, updated_at
FROM users_ext.enrollment_requests
WHERE (sqlc.narg(status)::enrollment_status IS NULL OR status = sqlc.narg(status)::enrollment_status)
ORDER BY created_at DESC;

-- name: AcceptEnrollment :one
-- Zero rows (pgx.ErrNoRows) = wrong id, wrong owner, or not invited.
SELECT * FROM users_ext.accept_enrollment(
    sqlc.arg(id)::uuid,
    sqlc.arg(account_id)::uuid,
    sqlc.arg(tos_version)
);

-- name: DeclineEnrollment :one
SELECT * FROM users_ext.decline_enrollment(
    sqlc.arg(id)::uuid,
    sqlc.arg(account_id)::uuid
);

-- name: CancelEnrollment :one
SELECT * FROM users_ext.cancel_enrollment(
    sqlc.arg(id)::uuid,
    sqlc.arg(decided_by)::uuid
);

-- name: ApproveEnrollment :one
SELECT * FROM users_ext.approve_enrollment(
    sqlc.arg(id)::uuid,
    sqlc.arg(decided_by)::uuid
);
