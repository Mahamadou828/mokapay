// Package repo owns the notification domain's persistence: the
// notifications and webpush_subscriptions tables. Reads go through the
// users_ext views, writes through the users_ext procedures.
package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"mokapay.app/app/services/user/internal/business/notification"
	"mokapay.app/app/services/user/internal/sqlc"
	coredb "mokapay.app/core/db"
)

type Store struct {
	sqlc *sqlc.Queries
}

func New(db *coredb.Postgres) Store {
	return Store{sqlc: sqlc.New(db.PgxPool)}
}

// AddNotification writes one inbox row — the source of truth of a
// notification, whatever happens to its push delivery.
func (s Store) AddNotification(ctx context.Context, nn notification.NewNotification) (notification.Notification, error) {
	row, err := s.sqlc.AddNotification(ctx, sqlc.AddNotificationParams{
		AccountID: nn.AccountID.String(),
		EventType: nn.EventType,
		Title:     nn.Title,
		Body:      nn.Body,
		Data:      toJSONB(nn.Data),
	})
	if err != nil {
		return notification.Notification{}, fmt.Errorf("adding notification: %w", err)
	}

	return notificationToDomain(row)
}

// ListNotifications returns the account's inbox, newest first, capped at
// limit rows.
func (s Store) ListNotifications(ctx context.Context, accountID string, f notification.Filter, limit int32) ([]notification.Notification, error) {
	rows, err := s.sqlc.ListNotifications(ctx, sqlc.ListNotificationsParams{
		AccountID: accountID,
		Status:    filterToParams(f),
		MaxRows:   limit,
	})
	if err != nil {
		return nil, fmt.Errorf("listing notifications: %w", err)
	}

	out := make([]notification.Notification, 0, len(rows))
	for _, row := range rows {
		n, err := viewNotificationToDomain(row)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}

	return out, nil
}

// MarkRead flips one notification to read, idempotently.
// notification.ErrNotificationNotFound covers unknown ids and foreign owners
// alike.
func (s Store) MarkRead(ctx context.Context, id, accountID string) (notification.Notification, error) {
	row, err := s.sqlc.MarkNotificationRead(ctx, sqlc.MarkNotificationReadParams{
		ID:        id,
		AccountID: accountID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return notification.Notification{}, notification.ErrNotificationNotFound
		}
		return notification.Notification{}, fmt.Errorf("marking notification %s read: %w", id, err)
	}

	return notificationToDomain(row)
}

// MarkAllRead flips every unread notification of the account; returns how
// many were flipped.
func (s Store) MarkAllRead(ctx context.Context, accountID string) (int64, error) {
	n, err := s.sqlc.MarkAllNotificationsRead(ctx, accountID)
	if err != nil {
		return 0, fmt.Errorf("marking all notifications read: %w", err)
	}
	return n, nil
}

// AddSubscription registers (or refreshes) one device's push route —
// upserts on the endpoint.
func (s Store) AddSubscription(ctx context.Context, ns notification.NewPushSubscription) (notification.PushSubscription, error) {
	row, err := s.sqlc.AddWebpushSubscription(ctx, sqlc.AddWebpushSubscriptionParams{
		AccountID: ns.AccountID.String(),
		Endpoint:  ns.Endpoint,
		P256dh:    ns.P256dh,
		Auth:      ns.Auth,
		UserAgent: toText(ns.UserAgent),
	})
	if err != nil {
		return notification.PushSubscription{}, fmt.Errorf("adding webpush subscription: %w", err)
	}

	return subscriptionToDomain(row.ID, row.AccountFk, row.Endpoint, row.P256dh, row.Auth,
		row.UserAgent, row.CreatedAt, row.UpdatedAt)
}

// ListSubscriptions returns every live delivery route of the account,
// oldest first.
func (s Store) ListSubscriptions(ctx context.Context, accountID string) ([]notification.PushSubscription, error) {
	rows, err := s.sqlc.ListWebpushSubscriptions(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("listing webpush subscriptions: %w", err)
	}

	out := make([]notification.PushSubscription, 0, len(rows))
	for _, row := range rows {
		sub, err := subscriptionToDomain(row.ID, row.AccountFk, row.Endpoint, row.P256dh, row.Auth,
			row.UserAgent, row.CreatedAt, row.UpdatedAt)
		if err != nil {
			return nil, err
		}
		out = append(out, sub)
	}

	return out, nil
}

// DeleteSubscription soft-deletes one route; unknown endpoint or foreign
// owner is a no-op (unsubscribe and gone-pruning are both idempotent).
func (s Store) DeleteSubscription(ctx context.Context, accountID, endpoint string) error {
	if err := s.sqlc.DeleteWebpushSubscription(ctx, sqlc.DeleteWebpushSubscriptionParams{
		AccountID: accountID,
		Endpoint:  endpoint,
	}); err != nil {
		return fmt.Errorf("deleting webpush subscription: %w", err)
	}
	return nil
}

// notificationToDomain converts a base-table row (write procedures return
// notifications).
func notificationToDomain(n sqlc.Notification) (notification.Notification, error) {
	return toDomain(n.ID, n.AccountFk, n.EventType, n.Title, n.Body, n.Data,
		n.Status, n.ReadAt, n.CreatedAt, n.UpdatedAt)
}

// viewNotificationToDomain converts a users_ext.notifications row.
func viewNotificationToDomain(n sqlc.UsersExtNotification) (notification.Notification, error) {
	return toDomain(n.ID, n.AccountFk, n.EventType, n.Title, n.Body, n.Data,
		n.Status, n.ReadAt, n.CreatedAt, n.UpdatedAt)
}

func toDomain(
	id, accountFk, eventType, title, body string, data []byte,
	status sqlc.NotificationStatus, readAt pgtype.Timestamptz,
	createdAt, updatedAt time.Time,
) (notification.Notification, error) {
	nid, err := uuid.Parse(id)
	if err != nil {
		return notification.Notification{}, fmt.Errorf("parsing notification id %q: %w", id, err)
	}
	account, err := uuid.Parse(accountFk)
	if err != nil {
		return notification.Notification{}, fmt.Errorf("parsing account id %q: %w", accountFk, err)
	}

	out := notification.Notification{
		ID:        nid,
		AccountID: account,
		EventType: eventType,
		Title:     title,
		Body:      body,
		Status:    notification.Status(status),
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	}
	if len(data) > 0 {
		out.Data = new(string(data))
	}
	if readAt.Valid {
		out.ReadAt = new(readAt.Time)
	}

	return out, nil
}

func subscriptionToDomain(
	id, accountFk, endpoint, p256dh, auth string,
	userAgent pgtype.Text, createdAt, updatedAt time.Time,
) (notification.PushSubscription, error) {
	sid, err := uuid.Parse(id)
	if err != nil {
		return notification.PushSubscription{}, fmt.Errorf("parsing subscription id %q: %w", id, err)
	}
	account, err := uuid.Parse(accountFk)
	if err != nil {
		return notification.PushSubscription{}, fmt.Errorf("parsing account id %q: %w", accountFk, err)
	}

	out := notification.PushSubscription{
		ID:        sid,
		AccountID: account,
		Endpoint:  endpoint,
		P256dh:    p256dh,
		Auth:      auth,
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	}
	if userAgent.Valid {
		out.UserAgent = new(userAgent.String)
	}

	return out, nil
}

// filterToParams maps the optional status filter to the sqlc null type —
// nil means the filter is absent.
func filterToParams(f notification.Filter) sqlc.NullNotificationStatus {
	if f.Status == nil {
		return sqlc.NullNotificationStatus{}
	}
	return sqlc.NullNotificationStatus{NotificationStatus: sqlc.NotificationStatus(*f.Status), Valid: true}
}

func toJSONB(s *string) []byte {
	if s == nil {
		return nil
	}
	return []byte(*s)
}

func toText(s *string) pgtype.Text {
	if s == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *s, Valid: true}
}
