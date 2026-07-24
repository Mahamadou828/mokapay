// Package auth is the transport layer of the AuthService: it validates
// requests, delegates to the usecase, and maps results back to proto.
package auth

import (
	"mokapay.app/app/services/user/internal/business/auth/usecase"
	"mokapay.app/core/logger"
	userv1 "mokapay.app/proto/codebase/user/v1"
)

type Handler struct {
	userv1.UnimplementedAuthServiceServer

	logger *logger.Logger
	uc     usecase.Auth
}

// NewHandler wires the transport to an already-built usecase — the usecase
// needs the Cognito client, the SMS sender and the OTP pepper, all of which
// are constructed once in the app composition layer.
func NewHandler(logger *logger.Logger, uc usecase.Auth) Handler {
	return Handler{
		logger: logger,
		uc:     uc,
	}
}
