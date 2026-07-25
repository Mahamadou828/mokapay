// Package admin is the transport layer of the AdminService (OPS/ADMIN
// moderation tier). It shares the account business types and proto mapper
// with the self-service api — route tiers on the gateway are the first
// gate; handlers stay defensive about their inputs.
package admin

import (
	usecase "mokapay.app/app/services/user/internal/business/auth/usecase/admin"
	enrollmentuc "mokapay.app/app/services/user/internal/business/enrollment/usecase"
	"mokapay.app/core/logger"
	userv1 "mokapay.app/proto/codebase/user/v1"
)

type Handler struct {
	userv1.UnimplementedAdminServiceServer

	logger *logger.Logger
	uc     usecase.Admin
	enroll enrollmentuc.UseCase
}

func NewHandler(logger *logger.Logger, uc usecase.Admin, enroll enrollmentuc.UseCase) Handler {
	return Handler{
		logger: logger,
		uc:     uc,
		enroll: enroll,
	}
}
