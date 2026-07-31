// Package live is the gateway's websocket surface: one authenticated socket
// per app session, fed by the shared redis event bus. It is a transport, not
// a domain — no request ever reaches a downstream service through it; the
// events it pushes tell a client that something it already knows how to read
// has changed.
package live

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/redis/go-redis/v9"
	"mokapay.app/app/services/gateway/internal/middleware"
	"mokapay.app/core/client"
	coreweb "mokapay.app/core/http"
	"mokapay.app/core/logger"
	"mokapay.app/core/ws"
)

// Bus is the subscribe half of the shared event bus (core/cache's client
// satisfies it). Every gateway replica subscribes to the same channel and
// pushes to its own connected clients — redis does the replica fan-out.
type Bus interface {
	Subscribe(ctx context.Context, channel string) *redis.PubSub
}

// Handler owns the upgrade handshake and the bus→hub dispatch loop.
type Handler struct {
	log  *logger.Logger
	hub  *ws.Hub
	auth middleware.AuthValidator
	moka *client.MokaClient
	bus  Bus
}

func NewHandler(log *logger.Logger, hub *ws.Hub, auth middleware.AuthValidator, moka *client.MokaClient, bus Bus) *Handler {
	return &Handler{log: log, hub: hub, auth: auth, moka: moka, bus: bus}
}

// Routes mounts the socket. It bypasses the Encoder pipeline on purpose: an
// upgraded connection hijacks the response and outlives the request timeout,
// so it cannot be a core/http HandlerFunc — which also means the handshake
// authenticates itself instead of sitting behind the Auth middleware.
func (h *Handler) Routes(s *coreweb.Server) {
	s.RegisterRawHandler(http.MethodGet, "/v1/ws", h.connect)
}

// connect authenticates the handshake, then upgrades. A browser's WebSocket
// constructor cannot set an Authorization header, so the access token rides
// the query string (fine over TLS) — but it is validated by the exact same
// AuthValidator + account rehydration as every REST route, and any failure
// answers with a plain HTTP error, before the protocol switch.
func (h *Handler) connect(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	token := bearerToken(r)
	if token == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	pass, err := middleware.Authenticate(ctx, h.auth, h.moka, token)
	if err != nil {
		h.log.Info(ctx, "live-connect", "status", "rejected unauthenticated socket", "error", err)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	if err := h.hub.Upgrade(w, r, pass.User.ID); err != nil {
		h.log.Error(ctx, "live-connect", "failed to upgrade socket", "account_id", pass.User.ID, "error", err)
		return
	}

	h.log.Info(ctx, "live-connect", "status", "socket opened", "account_id", pass.User.ID,
		"connections", h.hub.Count(pass.User.ID))
}

// Run is the dispatch loop: every envelope on the shared channel goes to the
// addressee's sockets on THIS replica (none, usually — that is not an error).
// It owns one goroutine for the process's lifetime; the caller cancels ctx to
// stop it.
func (h *Handler) Run(ctx context.Context) error {
	sub := h.bus.Subscribe(ctx, ws.Channel)
	defer func() {
		if err := sub.Close(); err != nil {
			h.log.Error(ctx, "live-bus", "failed to close subscription", "error", err)
		}
	}()

	h.log.Info(ctx, "live-bus", "status", "subscribed", "channel", ws.Channel)

	// Channel() reconnects underneath us — a redis blip costs dropped
	// events, never the subscription.
	events := sub.Channel()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg, ok := <-events:
			if !ok {
				return errors.New("live: event bus closed")
			}
			delivered, err := h.hub.Dispatch(msg.Payload)
			if err != nil {
				h.log.Error(ctx, "live-bus", "failed to dispatch event", "error", err)
				continue
			}
			if delivered > 0 {
				h.log.Info(ctx, "live-bus", "status", "event delivered", "connections", delivered)
			}
		}
	}
}

// bearerToken reads the access token from the Authorization header when the
// client can set one (native clients, tests) and falls back to the ?token=
// query param browsers are limited to.
func bearerToken(r *http.Request) string {
	if h := r.Header.Get("Authorization"); h != "" {
		if token, ok := strings.CutPrefix(h, "Bearer "); ok {
			return strings.TrimSpace(token)
		}
		return ""
	}
	return strings.TrimSpace(r.URL.Query().Get("token"))
}
