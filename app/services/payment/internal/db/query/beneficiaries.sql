-- name: AddBeneficiary :one
-- Upsert on the live (client, phone) pair.
SELECT * FROM payments_ext.add_beneficiary(
    sqlc.arg(client_nk)::uuid,
    sqlc.arg(name),
    sqlc.arg(phone),
    sqlc.arg(operator)::payment_operator,
    sqlc.narg(city)
);

-- name: GetBeneficiary :one
SELECT id, client_nk, name, phone, operator, city, created_at, updated_at
FROM payments_ext.beneficiaries
WHERE id = sqlc.arg(id)::uuid;

-- name: ListBeneficiaries :many
SELECT id, client_nk, name, phone, operator, city, created_at, updated_at
FROM payments_ext.beneficiaries
WHERE client_nk = sqlc.arg(client_nk)::uuid
ORDER BY created_at DESC;

-- name: UpdateBeneficiary :one
-- Owner-scoped partial update; a NULL argument keeps the stored value.
SELECT * FROM payments_ext.update_beneficiary(
    sqlc.arg(id)::uuid,
    sqlc.arg(client_nk)::uuid,
    sqlc.narg(name),
    sqlc.narg(phone),
    sqlc.narg(operator)::payment_operator,
    sqlc.narg(city)
);

-- name: DeleteBeneficiary :exec
SELECT payments_ext.delete_beneficiary(
    sqlc.arg(id)::uuid,
    sqlc.arg(client_nk)::uuid
);
