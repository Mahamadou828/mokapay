// Package api wires every HTTP route of the gateway. The root internal
// package builds the server and calls Register — api never imports the root,
// keeping the import direction one-way (internal → api → business/core).
package api

import (
	"context"
	"errors"
	"net/http"

	complianceapi "mokapay.app/app/services/gateway/internal/api/compliance"
	paymentapi "mokapay.app/app/services/gateway/internal/api/payment"
	userapi "mokapay.app/app/services/gateway/internal/api/user"
	"mokapay.app/app/services/gateway/internal/middleware"
	"mokapay.app/core/client"
	coreweb "mokapay.app/core/http"
	"mokapay.app/core/logger"
)

// Config carries the settings the API layer needs to wire its handlers.
type Config struct {
	// Moka is the shared downstream gRPC client (owned and closed by the
	// caller).
	Moka *client.MokaClient
	// Auth is the token-validation + passport-rehydration middleware,
	// applied per tier — never globally, the public auth routes are
	// anonymous by design.
	Auth coreweb.MidFunc
}

// Close releases the resources held by the registered handlers.
type Close func(ctx context.Context)

// Register mounts every v1 route on the server and returns a Close function
// the caller runs on shutdown.
func Register(cfg Config, log *logger.Logger, s *coreweb.Server) (Close, error) {
	if cfg.Moka == nil || cfg.Auth == nil {
		return nil, errors.New("api: Moka client and Auth middleware are required")
	}

	// -------------------------------------------------------------------------
	// Route tiers. Empty group paths keep the /v1 prefix while layering
	// middleware: public (anonymous), authed (valid token), ops (+role).
	v1 := s.NewGroup("/v1")
	authed := v1.NewGroup("", cfg.Auth)
	agent := v1.NewGroup("", cfg.Auth, middleware.RequireRoles("agent", "pending_agent"))
	ops := v1.NewGroup("", cfg.Auth, middleware.RequireRoles("ops", "admin"))

	// -------------------------------------------------------------------------
	// health routes
	v1.RegisterHandler(http.MethodGet, "/readiness", func(ctx context.Context, r *http.Request) coreweb.Encoder {
		return nil
	})

	v1.RegisterHandler(http.MethodGet, "/liveness", func(ctx context.Context, r *http.Request) coreweb.Encoder {
		return nil
	})

	// -------------------------------------------------------------------------
	// user service surface (auth flows, self-service, account moderation)
	userapi.NewHandler(log, cfg.Moka).Routes(v1, authed, ops)

	// -------------------------------------------------------------------------
	// payment service surface (transfer flow, agent inbox/wallets, ops)
	paymentapi.NewHandler(log, cfg.Moka).Routes(authed, agent, ops)

	// -------------------------------------------------------------------------
	// compliance service surface (AML alerts, cases, risk) — ops only. The
	// rule engine's EvaluateTransfer is service-internal and gets no route.
	complianceapi.NewHandler(log, cfg.Moka).Routes(ops)

	return func(ctx context.Context) {}, nil
}
