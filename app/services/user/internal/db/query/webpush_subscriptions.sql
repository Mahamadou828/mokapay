-- name: AddWebpushSubscription :one
SELECT * FROM users_ext.add_webpush_subscription(
    sqlc.arg(account_id)::uuid,
    sqlc.arg(endpoint),
    sqlc.arg(p256dh),
    sqlc.arg(auth),
    sqlc.narg(user_agent)
);

-- name: ListWebpushSubscriptions :many
-- Every delivery route of the account, oldest first (stable send order).
SELECT id, account_fk, endpoint, p256dh, auth, user_agent, created_at, updated_at
FROM users_ext.webpush_subscriptions
WHERE account_fk = sqlc.arg(account_id)::uuid
ORDER BY created_at;

-- name: DeleteWebpushSubscription :exec
SELECT users_ext.delete_webpush_subscription(
    sqlc.arg(account_id)::uuid,
    sqlc.arg(endpoint)
);
