-- name: AddNotification :one
SELECT * FROM users_ext.add_notification(
    sqlc.arg(account_id)::uuid,
    sqlc.arg(event_type),
    sqlc.arg(title),
    sqlc.arg(body),
    sqlc.narg(data)
);

-- name: ListNotifications :many
-- The inbox pull, newest first. The status filter is optional: NULL
-- disables it.
SELECT id, account_fk, event_type, title, body, data, status, read_at, created_at, updated_at
FROM users_ext.notifications
WHERE account_fk = sqlc.arg(account_id)::uuid
  AND (sqlc.narg(status)::notification_status IS NULL OR status = sqlc.narg(status)::notification_status)
ORDER BY created_at DESC
LIMIT sqlc.arg(max_rows);

-- name: MarkNotificationRead :one
-- Zero rows (pgx.ErrNoRows) = unknown id or not the caller's notification.
SELECT * FROM users_ext.mark_notification_read(
    sqlc.arg(id)::uuid,
    sqlc.arg(account_id)::uuid
);

-- name: MarkAllNotificationsRead :one
SELECT users_ext.mark_all_notifications_read(sqlc.arg(account_id)::uuid);
