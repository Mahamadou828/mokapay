package user

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"mokapay.app/app/services/gateway/internal/api/param"
	"mokapay.app/app/services/gateway/internal/sdk/errs"
	"mokapay.app/app/services/gateway/internal/sdk/protocodec"
	coreerrs "mokapay.app/core/error"
	core "mokapay.app/core/http"
	userv1 "mokapay.app/proto/codebase/user/v1"
)

// PushNotification is deliberately absent: it is the service-internal
// producer entrypoint (system passport only), never a REST route.

func (h Handler) getVapidPublicKey(ctx context.Context, _ *http.Request) core.Encoder {
	res, err := h.moka.User.Notification.GetVapidPublicKey(ctx, &userv1.GetVapidPublicKeyRequest{})
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

func (h Handler) listNotifications(ctx context.Context, r *http.Request) core.Encoder {
	status, err := param.Enum[userv1.NotificationStatus](r, "status", userv1.NotificationStatus_value)
	if err != nil {
		return coreerrs.New(errs.InvalidArgument, err)
	}

	req := &userv1.ListNotificationsRequest{Status: status}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err := strconv.ParseInt(raw, 10, 32)
		if err != nil {
			return coreerrs.New(errs.InvalidArgument, errors.New("invalid limit"))
		}
		req.Limit = int32(limit)
	}

	res, err := h.moka.User.Notification.ListNotifications(ctx, req)
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

func (h Handler) markNotificationRead(ctx context.Context, r *http.Request) core.Encoder {
	res, err := h.moka.User.Notification.MarkNotificationRead(ctx, &userv1.MarkNotificationReadRequest{
		Id: core.Param(r, "id"),
	})
	if err != nil {
		return rpcErr(err)
	}

	return protocodec.Proto{Message: res}
}

func (h Handler) markAllNotificationsRead(ctx context.Context, _ *http.Request) core.Encoder {
	if _, err := h.moka.User.Notification.MarkAllNotificationsRead(ctx, &userv1.MarkAllNotificationsReadRequest{}); err != nil {
		return rpcErr(err)
	}

	return nil // 204
}

func (h Handler) subscribePush(ctx context.Context, r *http.Request) core.Encoder {
	req := &userv1.SubscribePushRequest{}
	if err := core.Decode(r, protocodec.Proto{Message: req}); err != nil {
		return coreerrs.New(errs.InvalidArgument, err)
	}
	// The device hint comes from the transport, never the body.
	if ua := r.UserAgent(); ua != "" {
		req.UserAgent = new(ua)
	}

	if _, err := h.moka.User.Notification.SubscribePush(ctx, req); err != nil {
		return rpcErr(err)
	}

	return nil // 204
}

func (h Handler) unsubscribePush(ctx context.Context, r *http.Request) core.Encoder {
	req := &userv1.UnsubscribePushRequest{}
	if err := core.Decode(r, protocodec.Proto{Message: req}); err != nil {
		return coreerrs.New(errs.InvalidArgument, err)
	}

	if _, err := h.moka.User.Notification.UnsubscribePush(ctx, req); err != nil {
		return rpcErr(err)
	}

	return nil // 204
}
