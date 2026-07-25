// Package mapper converts between the notification business types and
// their proto counterparts.
package mapper

import (
	"google.golang.org/protobuf/types/known/timestamppb"
	"mokapay.app/app/services/user/internal/business/notification"
	userv1 "mokapay.app/proto/codebase/user/v1"
)

func NotificationToProto(in notification.Notification) *userv1.Notification {
	out := &userv1.Notification{
		Id:        in.ID.String(),
		AccountId: in.AccountID.String(),
		EventType: in.EventType,
		Title:     in.Title,
		Body:      in.Body,
		Data:      in.Data,
		Status:    StatusToProto(in.Status),
		CreatedAt: timestamppb.New(in.CreatedAt),
		UpdatedAt: timestamppb.New(in.UpdatedAt),
	}
	if in.ReadAt != nil {
		out.ReadAt = timestamppb.New(*in.ReadAt)
	}

	return out
}

func StatusToProto(in notification.Status) userv1.NotificationStatus {
	switch in {
	case notification.StatusUnread:
		return userv1.NotificationStatus_NOTIFICATION_STATUS_UNREAD
	case notification.StatusRead:
		return userv1.NotificationStatus_NOTIFICATION_STATUS_READ
	}
	return userv1.NotificationStatus_NOTIFICATION_STATUS_UNSPECIFIED
}

// StatusFromProto returns nil for UNSPECIFIED — "not filtering".
func StatusFromProto(in userv1.NotificationStatus) *notification.Status {
	switch in {
	case userv1.NotificationStatus_NOTIFICATION_STATUS_UNREAD:
		return new(notification.StatusUnread)
	case userv1.NotificationStatus_NOTIFICATION_STATUS_READ:
		return new(notification.StatusRead)
	}
	return nil
}
