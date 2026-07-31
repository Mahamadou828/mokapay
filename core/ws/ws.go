// Package ws is the shared websocket layer: a per-account registry of live
// client connections (the Hub) plus the Event/Envelope pair services publish
// onto the shared bus for a hub to fan out. It is to server push what
// core/http is to request/response — transport plumbing with no domain
// knowledge.
//
// The hub never authenticates anything: it is handed an already-resolved
// account id, and whoever calls Upgrade is responsible for having validated
// the request exactly as strictly as any other authenticated route.
package ws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Logger mirrors core/http's logger shape so a service passes the same
// log.Info it already passes to the HTTP server.
type Logger func(ctx context.Context, msg string, args ...any)

const (
	// writeWait bounds a single frame write; a client that cannot absorb a
	// frame in that window is treated as gone.
	writeWait = 10 * time.Second
	// pongWait is how long a connection may stay silent before the read
	// side gives up on it (the client answers our pings).
	pongWait = 60 * time.Second
	// pingPeriod must stay comfortably below pongWait — the keepalive that
	// holds mobile NAT/proxy timeouts open.
	pingPeriod = 25 * time.Second
	// readLimit caps inbound frames. The contract is server push only, so
	// anything a client sends is small by definition.
	readLimit = 512
	// sendQueue is how far a connection may fall behind before the hub
	// drops it: a stalled client must never slow the fan-out down.
	sendQueue = 32
)

// Opts tunes the hub.
type Opts struct {
	// AllowedOrigins gates the upgrade by Origin header, mirroring the HTTP
	// server's CORS list. Empty (or a "*" entry) accepts any origin — the
	// socket is authenticated by a bearer token, never by a cookie, so a
	// cross-origin page gains nothing by opening one.
	AllowedOrigins []string
}

// Hub is the connection registry: one account maps to every socket it has
// open right now (several tabs, phone + laptop), and an event fans out to
// all of them. Application-unique and mutex-guarded — hold it as a pointer.
type Hub struct {
	log      Logger
	upgrader websocket.Upgrader

	mu    sync.RWMutex
	conns map[string]map[*conn]struct{}
}

// NewHub builds an empty hub.
func NewHub(log Logger, opts Opts) *Hub {
	return &Hub{
		log:   log,
		conns: make(map[string]map[*conn]struct{}),
		upgrader: websocket.Upgrader{
			HandshakeTimeout: 10 * time.Second,
			ReadBufferSize:   1024,
			WriteBufferSize:  4096,
			CheckOrigin:      originChecker(opts.AllowedOrigins),
		},
	}
}

// Upgrade switches the request to the websocket protocol and registers the
// resulting connection under accountID. Every error is returned before the
// protocol switch, so the caller can still answer with plain HTTP.
func (h *Hub) Upgrade(w http.ResponseWriter, r *http.Request, accountID string) error {
	if accountID == "" {
		return errors.New("upgrade requires an authenticated account id")
	}

	ws, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		// Upgrade already wrote the HTTP error response.
		return fmt.Errorf("upgrading connection: %w", err)
	}

	// The request context is cancelled the moment the handler returns, even
	// for a hijacked connection — WithoutCancel keeps the trace values for
	// the pump logs while dropping that cancellation.
	ctx := context.WithoutCancel(r.Context())

	c := &conn{
		ws:   ws,
		send: make(chan []byte, sendQueue),
		done: make(chan struct{}),
	}
	h.register(accountID, c)

	go c.writePump()
	go h.readPump(ctx, accountID, c)

	return nil
}

// Send fans an event out to every live connection of accountID and reports
// how many took it. Zero is the normal case for an offline account.
func (h *Hub) Send(accountID string, e Event) int {
	payload, err := json.Marshal(e)
	if err != nil {
		h.logf(context.Background(), "ws-send", "failed to encode event", "event_type", e.Type, "error", err)
		return 0
	}

	h.mu.RLock()
	targets := make([]*conn, 0, len(h.conns[accountID]))
	for c := range h.conns[accountID] {
		targets = append(targets, c)
	}
	h.mu.RUnlock()

	var sent int
	for _, c := range targets {
		if c.enqueue(payload) {
			sent++
			continue
		}
		// Closed or hopelessly behind: drop it here, the read pump prunes
		// the registry entry on its way out.
		c.close()
	}

	return sent
}

// Dispatch is the subscriber half of the fan-out: it decodes one bus
// payload and pushes it to its addressee's sockets. An envelope for an
// account with no live socket is not an error — most accounts are offline
// most of the time.
func (h *Hub) Dispatch(payload string) (int, error) {
	var env Envelope
	if err := json.Unmarshal([]byte(payload), &env); err != nil {
		return 0, fmt.Errorf("decoding envelope: %w", err)
	}
	if env.AccountID == "" || env.Type == "" {
		return 0, errors.New("envelope missing account_id or event_type")
	}

	return h.Send(env.AccountID, env.Event), nil
}

// Count reports how many live connections an account holds — metrics and
// tests, never a delivery decision.
func (h *Hub) Count(accountID string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.conns[accountID])
}

// Close drops every live connection: the shutdown path.
func (h *Hub) Close() {
	h.mu.Lock()
	accounts := h.conns
	h.conns = make(map[string]map[*conn]struct{})
	h.mu.Unlock()

	for _, set := range accounts {
		for c := range set {
			c.close()
		}
	}
}

func (h *Hub) register(accountID string, c *conn) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.conns[accountID] == nil {
		h.conns[accountID] = make(map[*conn]struct{})
	}
	h.conns[accountID][c] = struct{}{}
}

func (h *Hub) remove(accountID string, c *conn) {
	c.close()

	h.mu.Lock()
	defer h.mu.Unlock()

	set := h.conns[accountID]
	delete(set, c)
	if len(set) == 0 {
		delete(h.conns, accountID)
	}
}

// readPump owns the connection's lifetime: it keeps the pong deadline armed
// and prunes the registry the moment the socket dies. Inbound frames are
// not part of the contract (server push only) — reading them is what makes
// close and pong handling work.
func (h *Hub) readPump(ctx context.Context, accountID string, c *conn) {
	defer h.remove(accountID, c)

	c.ws.SetReadLimit(readLimit)
	if err := c.ws.SetReadDeadline(time.Now().Add(pongWait)); err != nil {
		return
	}
	c.ws.SetPongHandler(func(string) error {
		return c.ws.SetReadDeadline(time.Now().Add(pongWait))
	})

	for {
		if _, _, err := c.ws.ReadMessage(); err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				h.logf(ctx, "ws-read", "connection closed", "account_id", accountID, "error", err)
			}
			return
		}
		if err := c.ws.SetReadDeadline(time.Now().Add(pongWait)); err != nil {
			return
		}
	}
}

func (h *Hub) logf(ctx context.Context, op, msg string, args ...any) {
	if h.log == nil {
		return
	}
	h.log(ctx, msg, append([]any{"op", op}, args...)...)
}

// conn is one live socket. All writes go through its own goroutine: gorilla
// allows a single concurrent writer, and the queue in front of it keeps one
// slow client from stalling everyone else's events.
type conn struct {
	ws   *websocket.Conn
	send chan []byte
	done chan struct{}
	once sync.Once
}

func (c *conn) enqueue(payload []byte) bool {
	select {
	case <-c.done:
		return false
	default:
	}

	select {
	case c.send <- payload:
		return true
	default:
		return false // queue full — the caller drops the connection
	}
}

func (c *conn) close() {
	c.once.Do(func() {
		close(c.done)
		_ = c.ws.Close() // best-effort: the peer may already be gone
	})
}

func (c *conn) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-c.done:
			return

		case payload := <-c.send:
			if err := c.ws.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
				c.close()
				return
			}
			if err := c.ws.WriteMessage(websocket.TextMessage, payload); err != nil {
				c.close() // the read pump prunes the registry entry
				return
			}

		case <-ticker.C:
			if err := c.ws.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
				c.close()
				return
			}
			if err := c.ws.WriteMessage(websocket.PingMessage, nil); err != nil {
				c.close()
				return
			}
		}
	}
}

// originChecker mirrors the HTTP server's CORS list: no list (or "*")
// accepts any origin, and a request without an Origin header — every
// non-browser client — is always accepted.
func originChecker(allowed []string) func(*http.Request) bool {
	return func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" || len(allowed) == 0 {
			return true
		}
		for _, o := range allowed {
			if o == "*" || o == origin {
				return true
			}
		}
		return false
	}
}
