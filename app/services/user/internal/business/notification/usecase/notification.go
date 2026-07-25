// Package usecase implements the notification domain logic: the inbox row
// is the source of truth, the web push on top is best-effort delivery.
package usecase

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"mokapay.app/app/services/user/internal/business/notification"
	"mokapay.app/app/services/user/internal/business/notification/repo"
	coredb "mokapay.app/core/db"
	"mokapay.app/core/logger"
)

// PushSender delivers one payload to one subscription (satisfied by
// webpush.Sender and webpush.Log). notification.ErrSubscriptionGone marks a
// dead route to prune; any other error is that route's problem alone.
type PushSender interface {
	Send(ctx context.Context, sub notification.PushSubscription, payload []byte) error
}

type UseCase struct {
	logger         *logger.Logger
	repo           repo.Store
	sender         PushSender
	vapidPublicKey string
}

func New(l *logger.Logger, db *coredb.Postgres, sender PushSender, vapidPublicKey string) UseCase {
	return UseCase{
		logger:         l,
		repo:           repo.New(db),
		sender:         sender,
		vapidPublicKey: vapidPublicKey,
	}
}

// pushPayload is what the service worker receives (and the shape the
// frontend displays).
type pushPayload struct {
	Title     string          `json:"title"`
	Body      string          `json:"body"`
	EventType string          `json:"event_type"`
	Data      json.RawMessage `json:"data,omitempty"`
}

// Push writes the inbox row and best-effort delivers it to every live
// route of the account. Only the row write can fail the call: send errors
// are logged, dead routes are pruned, zero routes is fine.
func (u UseCase) Push(ctx context.Context, nn notification.NewNotification) (notification.Notification, error) {
	created, err := u.repo.AddNotification(ctx, nn)
	if err != nil {
		u.logger.Error(ctx, "Notification.Push: failed to add notification", "err", err)
		return notification.Notification{}, err
	}

	subs, err := u.repo.ListSubscriptions(ctx, nn.AccountID.String())
	if err != nil {
		// The inbox row exists — the notification happened. Delivery is lost
		// for this round only.
		u.logger.Error(ctx, "Notification.Push: failed to list subscriptions", "err", err, "account", nn.AccountID)
		return created, nil
	}

	payload, err := json.Marshal(pushPayload{
		Title:     created.Title,
		Body:      created.Body,
		EventType: created.EventType,
		Data:      toRawData(created.Data),
	})
	if err != nil {
		u.logger.Error(ctx, "Notification.Push: failed to marshal payload", "err", err)
		return created, nil
	}

	for _, sub := range subs {
		if err := u.sender.Send(ctx, sub, payload); err != nil {
			if errors.Is(err, notification.ErrSubscriptionGone) {
				// The push service says this route is dead — prune it.
				if err := u.repo.DeleteSubscription(ctx, sub.AccountID.String(), sub.Endpoint); err != nil {
					u.logger.Error(ctx, "Notification.Push: failed to prune gone subscription", "err", err, "endpoint", sub.Endpoint)
				}
				continue
			}
			u.logger.Error(ctx, "Notification.Push: send failed", "err", err, "endpoint", sub.Endpoint)
		}
	}

	return created, nil
}

// List returns the caller's inbox, newest first.
func (u UseCase) List(ctx context.Context, accountID uuid.UUID, f notification.Filter, limit int32) ([]notification.Notification, error) {
	out, err := u.repo.ListNotifications(ctx, accountID.String(), f, limit)
	if err != nil {
		u.logger.Error(ctx, "Notification.List: failed to list", "err", err)
		return nil, err
	}
	return out, nil
}

// MarkRead flips one of the caller's notifications, idempotently.
func (u UseCase) MarkRead(ctx context.Context, accountID, id uuid.UUID) (notification.Notification, error) {
	out, err := u.repo.MarkRead(ctx, id.String(), accountID.String())
	if err != nil {
		if !errors.Is(err, notification.ErrNotificationNotFound) {
			u.logger.Error(ctx, "Notification.MarkRead: failed", "err", err)
		}
		return notification.Notification{}, err
	}
	return out, nil
}

// MarkAllRead flips the caller's whole unread backlog.
func (u UseCase) MarkAllRead(ctx context.Context, accountID uuid.UUID) error {
	n, err := u.repo.MarkAllRead(ctx, accountID.String())
	if err != nil {
		u.logger.Error(ctx, "Notification.MarkAllRead: failed", "err", err)
		return err
	}
	u.logger.Info(ctx, "notifications marked read", "account", accountID, "count", n)
	return nil
}

// Subscribe registers (or refreshes) the calling device's push route.
func (u UseCase) Subscribe(ctx context.Context, ns notification.NewPushSubscription) (notification.PushSubscription, error) {
	out, err := u.repo.AddSubscription(ctx, ns)
	if err != nil {
		u.logger.Error(ctx, "Notification.Subscribe: failed", "err", err)
		return notification.PushSubscription{}, err
	}
	return out, nil
}

// Unsubscribe removes one of the caller's routes; unknown endpoints no-op.
func (u UseCase) Unsubscribe(ctx context.Context, accountID uuid.UUID, endpoint string) error {
	if err := u.repo.DeleteSubscription(ctx, accountID.String(), endpoint); err != nil {
		u.logger.Error(ctx, "Notification.Unsubscribe: failed", "err", err)
		return err
	}
	return nil
}

// VapidPublicKey is what the frontend feeds to PushManager.subscribe();
// empty when the service runs with the Log sender (dev mode).
func (u UseCase) VapidPublicKey() string {
	return u.vapidPublicKey
}

func toRawData(s *string) json.RawMessage {
	if s == nil {
		return nil
	}
	if !json.Valid([]byte(*s)) {
		// Stored data is JSONB — this can only happen on a programming
		// error; keep the push deliverable and drop the payload.
		return nil
	}
	return json.RawMessage(*s)
}
