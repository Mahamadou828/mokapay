// Package user translates the gateway's REST surface for the user service
// into gRPC calls: decode proto-JSON request → call the Moka client → encode
// the proto response. Identity travels implicitly — the Auth middleware put
// the passport in the context and the client interceptor forwards it.
package user

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
