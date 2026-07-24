// Package file is the transport layer of the FileService: it validates
// requests, enforces the caller policy, delegates to the usecase and maps
// results back to proto.
//
// Caller policy (design doc F2): the gateway injects the passport for every
// end-user call. A request WITHOUT a passport is a service-internal call
// (payment fetching a proof) and is trusted with reads; writes always
// require a passport. Passport-holding callers may only touch their own
// files unless their role is ops/admin.
package file

import (
	"mokapay.app/app/services/file/internal/business/file/usecase"
	"mokapay.app/core/logger"
	filev1 "mokapay.app/proto/codebase/file/v1"
)

type Handler struct {
	filev1.UnimplementedFileServiceServer

	logger *logger.Logger
	uc     usecase.UseCase
}

func NewHandler(logger *logger.Logger, uc usecase.UseCase) Handler {
	return Handler{
		logger: logger,
		uc:     uc,
	}
}
