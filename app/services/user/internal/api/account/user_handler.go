// Package account is the transport layer of the self-service UserService.
// Its mapper package is shared with api/admin — both serve the same account
// entity.
package account

import (
	"mokapay.app/app/services/user/internal/business/auth/usecase"
	"mokapay.app/core/logger"
	userv1 "mokapay.app/proto/codebase/user/v1"
)

// UserHandler serves the self-service RPCs; the caller is always the
// identity forwarded by the gateway in metadata.
type UserHandler struct {
	userv1.UnimplementedUserServiceServer

	logger *logger.Logger
	uc     usecase.Account
}

func NewUserHandler(logger *logger.Logger, uc usecase.Account) UserHandler {
	return UserHandler{
		logger: logger,
		uc:     uc,
	}
}
