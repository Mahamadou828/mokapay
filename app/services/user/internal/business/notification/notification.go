// Package notification holds the notification domain types: the in-app
// inbox rows (source of truth) and the web-push subscriptions (the delivery
// routes of an account's devices).
package notification

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrNotificationNotFound covers both an unknown id and a notification
// owned by another account — callers can't tell the two apart on purpose.
var ErrNotificationNotFound = errors.New("notification not found")

// ErrSubscriptionGone is returned by a push sender when the push service
// reports the subscription no longer exists (HTTP 404/410) — the caller
// prunes the stored route.
var ErrSubscriptionGone = errors.New("push subscription gone")

// Status values mirror the stored notification_status enum.
type Status string

const (
	StatusUnread Status = "unread"
	StatusRead   Status = "read"
)

// Notification is the read entity — one inbox row.
type Notification struct {
	ID        uuid.UUID
	AccountID uuid.UUID
	EventType string
	Title     string
	Body      string
	Data      *string // JSON payload, opaque to this service
	Status    Status
	ReadAt    *time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NewNotification is the push payload — what a producer must provide to
// notify an account.
type NewNotification struct {
	AccountID uuid.UUID
	EventType string
	Title     string
	Body      string
	Data      *string
}

// Filter narrows inbox listings; nil means "not filtering".
type Filter struct {
	Status *Status
}

// PushSubscription is one browser/device push registration, as issued by
// PushManager.subscribe() on the client.
type PushSubscription struct {
	ID        uuid.UUID
	AccountID uuid.UUID
	Endpoint  string
	P256dh    string
	Auth      string
	UserAgent *string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NewPushSubscription is the subscribe payload.
type NewPushSubscription struct {
	AccountID uuid.UUID
	Endpoint  string
	P256dh    string
	Auth      string
	UserAgent *string
}
