// Package compliance translates the gateway's REST surface for the
// compliance service into gRPC calls: decode proto-JSON request → call the
// Moka client → encode the proto response. Only the staff (AdminService)
// surface is mounted; ComplianceService.EvaluateTransfer is
// service-internal (payment's job calls it under a system passport) and
// deliberately has no gateway route.
package compliance

import (
	"errors"

	"google.golang.org/grpc/status"
	"mokapay.app/app/services/gateway/internal/sdk/errs"
	"mokapay.app/core/client"
	coreerrs "mokapay.app/core/error"
	core "mokapay.app/core/http"
	"mokapay.app/core/logger"
)

type Handler struct {
	logger *logger.Logger
	moka   *client.MokaClient
}

func NewHandler(logger *logger.Logger, moka *client.MokaClient) Handler {
	return Handler{
		logger: logger,
		moka:   moka,
	}
}

// rpcErr converts a downstream gRPC failure to the gateway's HTTP error
// shape, preserving the status message (business errors are already
// client-safe; internals were collapsed service-side).
func rpcErr(err error) core.Encoder {
	return coreerrs.New(errs.FromGRPC(err), errors.New(status.Convert(err).Message()))
}
