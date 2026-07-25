// Package notification exposes the NotificationService gRPC surface: the
// internal producer entrypoint (PushNotification, system passport only)
// and the self-service inbox/subscription RPCs (caller from the passport).
package notification

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"mokapay.app/app/services/user/internal/api/notification/mapper"
	"mokapay.app/app/services/user/internal/business/notification"
	"mokapay.app/app/services/user/internal/business/notification/usecase"
	"mokapay.app/app/services/user/internal/sdk/grpcerr"
	"mokapay.app/core/logger"
	"mokapay.app/core/passport"
	userv1 "mokapay.app/proto/codebase/user/v1"
)

const (
	defaultListLimit = 50
	maxListLimit     = 200
)

type Handler struct {
	userv1.UnimplementedNotificationServiceServer
	logger *logger.Logger
	uc     usecase.UseCase
}

func NewHandler(l *logger.Logger, uc usecase.UseCase) *Handler {
	return &Handler{logger: l, uc: uc}
}

// callerAccountID resolves the calling account from the gateway-rehydrated
// passport.
func callerAccountID(ctx context.Context) (uuid.UUID, error) {
	pass, err := passport.FromContext(ctx)
	if err != nil {
		return uuid.UUID{}, status.Error(codes.Unauthenticated, "no passport in context")
	}
	id, err := uuid.Parse(pass.User.ID)
	if err != nil {
		return uuid.UUID{}, status.Error(codes.Unauthenticated, "passport carries no account id")
	}
	return id, nil
}

func (h *Handler) PushNotification(ctx context.Context, req *userv1.PushNotificationRequest) (*userv1.Notification, error) {
	pass, err := passport.FromContext(ctx)
	if err != nil || !pass.IsSystem() {
		return nil, status.Error(codes.PermissionDenied, "push notification is service-internal")
	}

	accountID, err := uuid.Parse(req.AccountId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid account id")
	}
	if req.EventType == "" || req.Title == "" || req.Body == "" {
		return nil, status.Error(codes.InvalidArgument, "event_type, title and body are required")
	}
	if req.Data != nil && !json.Valid([]byte(*req.Data)) {
		return nil, status.Error(codes.InvalidArgument, "data must be valid JSON")
	}

	created, err := h.uc.Push(ctx, notification.NewNotification{
		AccountID: accountID,
		EventType: req.EventType,
		Title:     req.Title,
		Body:      req.Body,
		Data:      req.Data,
	})
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	return mapper.NotificationToProto(created), nil
}

func (h *Handler) ListNotifications(ctx context.Context, req *userv1.ListNotificationsRequest) (*userv1.ListNotificationsResponse, error) {
	accountID, err := callerAccountID(ctx)
	if err != nil {
		return nil, err
	}

	limit := req.Limit
	if limit <= 0 {
		limit = defaultListLimit
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}

	var filter notification.Filter
	if req.Status != nil {
		filter.Status = mapper.StatusFromProto(*req.Status)
	}

	list, err := h.uc.List(ctx, accountID, filter, limit)
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	out := make([]*userv1.Notification, 0, len(list))
	for _, n := range list {
		out = append(out, mapper.NotificationToProto(n))
	}

	return &userv1.ListNotificationsResponse{Notifications: out}, nil
}

func (h *Handler) MarkNotificationRead(ctx context.Context, req *userv1.MarkNotificationReadRequest) (*userv1.Notification, error) {
	accountID, err := callerAccountID(ctx)
	if err != nil {
		return nil, err
	}

	id, err := uuid.Parse(req.Id)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid notification id")
	}

	read, err := h.uc.MarkRead(ctx, accountID, id)
	if err != nil {
		return nil, grpcerr.Map(err)
	}

	return mapper.NotificationToProto(read), nil
}

func (h *Handler) MarkAllNotificationsRead(ctx context.Context, _ *userv1.MarkAllNotificationsReadRequest) (*emptypb.Empty, error) {
	accountID, err := callerAccountID(ctx)
	if err != nil {
		return nil, err
	}

	if err := h.uc.MarkAllRead(ctx, accountID); err != nil {
		return nil, grpcerr.Map(err)
	}

	return &emptypb.Empty{}, nil
}

func (h *Handler) SubscribePush(ctx context.Context, req *userv1.SubscribePushRequest) (*emptypb.Empty, error) {
	accountID, err := callerAccountID(ctx)
	if err != nil {
		return nil, err
	}

	if req.Endpoint == "" || req.P256Dh == "" || req.Auth == "" {
		return nil, status.Error(codes.InvalidArgument, "endpoint, p256dh and auth are required")
	}

	if _, err := h.uc.Subscribe(ctx, notification.NewPushSubscription{
		AccountID: accountID,
		Endpoint:  req.Endpoint,
		P256dh:    req.P256Dh,
		Auth:      req.Auth,
		UserAgent: req.UserAgent,
	}); err != nil {
		return nil, grpcerr.Map(err)
	}

	return &emptypb.Empty{}, nil
}

func (h *Handler) UnsubscribePush(ctx context.Context, req *userv1.UnsubscribePushRequest) (*emptypb.Empty, error) {
	accountID, err := callerAccountID(ctx)
	if err != nil {
		return nil, err
	}

	if req.Endpoint == "" {
		return nil, status.Error(codes.InvalidArgument, "endpoint is required")
	}

	if err := h.uc.Unsubscribe(ctx, accountID, req.Endpoint); err != nil {
		return nil, grpcerr.Map(err)
	}

	return &emptypb.Empty{}, nil
}

func (h *Handler) GetVapidPublicKey(_ context.Context, _ *userv1.GetVapidPublicKeyRequest) (*userv1.GetVapidPublicKeyResponse, error) {
	return &userv1.GetVapidPublicKeyResponse{PublicKey: h.uc.VapidPublicKey()}, nil
}
