// Package admin is the transport layer of the AdminService (OPS/ADMIN
// moderation tier). It shares the account business types and proto mapper
// with the self-service api — route tiers on the gateway are the first
// gate; handlers stay defensive about their inputs.
package admin

import (
	"mokapay.app/app/services/user/internal/business/auth/usecase"
	"mokapay.app/core/logger"
	userv1 "mokapay.app/proto/codebase/user/v1"
)

type Handler struct {
	userv1.UnimplementedAdminServiceServer

	logger *logger.Logger
	uc     usecase.Account
}

func NewHandler(logger *logger.Logger, uc usecase.Account) Handler {
	return Handler{
		logger: logger,
		uc:     uc,
	}
}
