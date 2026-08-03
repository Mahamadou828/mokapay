-- name: GetAccount :one
SELECT id, phone_number, cognito_sub, first_name, last_name, email, preferred_language,
       preferred_otp_method, role, status, last_login_at, created_at, updated_at
FROM users_ext.accounts
WHERE id = sqlc.arg(id)::uuid;

-- name: GetAccountByPhone :one
SELECT id, phone_number, cognito_sub, first_name, last_name, email, preferred_language,
       preferred_otp_method, role, status, last_login_at, created_at, updated_at
FROM users_ext.accounts
WHERE phone_number = sqlc.arg(phone_number);

-- name: GetAccountBySub :one
SELECT id, phone_number, cognito_sub, first_name, last_name, email, preferred_language,
       preferred_otp_method, role, status, last_login_at, created_at, updated_at
FROM users_ext.accounts
WHERE cognito_sub = sqlc.arg(cognito_sub);

-- name: ListAccounts :many
-- Every filter is optional: a NULL value disables it.
SELECT id, phone_number, cognito_sub, first_name, last_name, email, preferred_language,
       preferred_otp_method, role, status, last_login_at, created_at, updated_at
FROM users_ext.accounts
WHERE (sqlc.narg(role)::account_role IS NULL OR role = sqlc.narg(role)::account_role)
  AND (sqlc.narg(status)::account_status IS NULL OR status = sqlc.narg(status)::account_status)
  AND (sqlc.narg(phone_number)::text IS NULL OR phone_number = sqlc.narg(phone_number)::text)
ORDER BY created_at DESC;

-- name: AddAccount :one
SELECT * FROM users_ext.add_account(
    sqlc.arg(phone_number),
    sqlc.arg(first_name),
    sqlc.arg(last_name),
    sqlc.narg(email),
    sqlc.narg(preferred_language)::account_language,
    sqlc.narg(role)::account_role,
    sqlc.narg(status)::account_status,
    sqlc.narg(cognito_sub)
);

-- name: UpdateAccount :one
SELECT * FROM users_ext.update_account(
    sqlc.arg(id)::uuid,
    sqlc.narg(first_name),
    sqlc.narg(last_name),
    sqlc.narg(email),
    sqlc.narg(preferred_language)::account_language,
    sqlc.narg(preferred_otp_method)::otp_method,
    sqlc.narg(role)::account_role,
    sqlc.narg(status)::account_status,
    sqlc.narg(last_login_at),
    sqlc.narg(cognito_sub)
);

-- name: DeleteAccount :exec
SELECT users_ext.delete_account(sqlc.arg(id)::uuid);
