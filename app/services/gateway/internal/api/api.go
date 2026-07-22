// Package api wires every HTTP route of the gateway. The root internal
// package builds the server and calls Register — api never imports the root,
// keeping the import direction one-way (internal → api → business/core).
package api

import (
	"context"
	"net/http"

	coreweb "template.app/core/http"
	"template.app/core/logger"
)

// Config carries the settings the API layer needs to wire its handlers
// (downstream service addresses, feature flags, …).
type Config struct{}

// Close releases the resources held by the registered handlers (gRPC conns).
type Close func(ctx context.Context)

// Register mounts every v1 route on the server and returns a Close function
// the caller runs on shutdown.
func Register(cfg Config, log *logger.Logger, s *coreweb.Server) (Close, error) {
	// -------------------------------------------------------------------------
	// create main v1 group
	v1 := s.NewGroup("/v1")

	// -------------------------------------------------------------------------
	// create readiness route
	v1.RegisterHandler(http.MethodGet, "/readiness", func(ctx context.Context, r *http.Request) coreweb.Encoder {
		return nil
	})

	v1.RegisterHandler(http.MethodGet, "/liveness", func(ctx context.Context, r *http.Request) coreweb.Encoder {
		return nil
	})

	// -------------------------------------------------------------------------
	// Service routes go here: init the downstream gRPC client, build the
	// domain handlers, mount them on a sub-group of v1, and close the client
	// connection in the returned Close function.

	return func(ctx context.Context) {}, nil
}
