// Package webpush delivers notifications to browser push services (Web
// Push protocol, VAPID). Sender is the real implementation; Log is the dev
// stand-in used when no VAPID keys are configured.
package webpush

import (
	"context"
	"fmt"
	"net/http"
	"time"

	wp "github.com/SherClockHolmes/webpush-go"
	"mokapay.app/app/services/user/internal/business/notification"
)

// Sender pushes payloads through the subscription's push service.
type Sender struct {
	vapidPublicKey  string
	vapidPrivateKey string
	subscriber      string // contact for the push service (mailto:), VAPID sub claim
	ttl             int    // seconds the push service may hold an undelivered message
}

func NewSender(publicKey, privateKey, subscriber string, ttl time.Duration) Sender {
	return Sender{
		vapidPublicKey:  publicKey,
		vapidPrivateKey: privateKey,
		subscriber:      subscriber,
		ttl:             int(ttl.Seconds()),
	}
}

// Send implements usecase.PushSender. notification.ErrSubscriptionGone
// signals a dead route (device unsubscribed / browser reset) the caller
// should prune.
func (s Sender) Send(ctx context.Context, sub notification.PushSubscription, payload []byte) error {
	resp, err := wp.SendNotificationWithContext(ctx, payload, &wp.Subscription{
		Endpoint: sub.Endpoint,
		Keys: wp.Keys{
			P256dh: sub.P256dh,
			Auth:   sub.Auth,
		},
	}, &wp.Options{
		VAPIDPublicKey:  s.vapidPublicKey,
		VAPIDPrivateKey: s.vapidPrivateKey,
		Subscriber:      s.subscriber,
		TTL:             s.ttl,
	})
	if err != nil {
		return fmt.Errorf("sending web push: %w", err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusNotFound, resp.StatusCode == http.StatusGone:
		return notification.ErrSubscriptionGone
	case resp.StatusCode >= http.StatusBadRequest:
		return fmt.Errorf("push service replied %d", resp.StatusCode)
	}

	return nil
}
