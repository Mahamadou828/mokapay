-- name: GetFile :one
SELECT id, owner_nk, purpose, related_type, related_nk, bucket, storage_key,
       file_name, content_type, size_bytes, sha256, etag, status,
       created_at, updated_at
FROM files_ext.files
WHERE id = sqlc.arg(id)::uuid;

-- name: ListFiles :many
-- The purpose filter is optional: a NULL value disables it.
SELECT id, owner_nk, purpose, related_type, related_nk, bucket, storage_key,
       file_name, content_type, size_bytes, sha256, etag, status,
       created_at, updated_at
FROM files_ext.files
WHERE related_type = sqlc.arg(related_type)
  AND related_nk = sqlc.arg(related_nk)::uuid
  AND (sqlc.narg(purpose)::file_purpose IS NULL OR purpose = sqlc.narg(purpose)::file_purpose)
ORDER BY created_at DESC;

-- name: ListExpiredPendingFiles :many
-- The orphan sweep: pending uploads created before the cutoff.
SELECT id, owner_nk, purpose, related_type, related_nk, bucket, storage_key,
       file_name, content_type, size_bytes, sha256, etag, status,
       created_at, updated_at
FROM files_ext.files
WHERE status = 'pending'
  AND created_at < sqlc.arg(before)
ORDER BY created_at
LIMIT sqlc.arg(max_rows);

-- name: AddFile :one
SELECT * FROM files_ext.add_file(
    sqlc.arg(owner_nk)::uuid,
    sqlc.arg(purpose)::file_purpose,
    sqlc.arg(bucket),
    sqlc.arg(storage_key),
    sqlc.arg(file_name),
    sqlc.arg(content_type),
    sqlc.arg(size_bytes),
    sqlc.narg(related_type),
    sqlc.narg(related_nk)::uuid
);

-- name: ConfirmFile :one
-- Zero rows (sql.ErrNoRows) = not pending anymore (double confirm, rejected,
-- or swept).
SELECT * FROM files_ext.confirm_file(
    sqlc.arg(id)::uuid,
    sqlc.arg(sha256),
    sqlc.arg(etag)
);

-- name: RejectFile :one
-- Zero rows (sql.ErrNoRows) = not currently stored.
SELECT * FROM files_ext.reject_file(sqlc.arg(id)::uuid);

-- name: DeleteFile :exec
SELECT files_ext.delete_file(sqlc.arg(id)::uuid);
