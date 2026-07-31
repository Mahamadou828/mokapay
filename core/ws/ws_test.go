package ws_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"mokapay.app/core/ws"
)

// hubServer stands the hub behind a real HTTP test server: every test here
// goes over a real socket, because "the registry looks right" is not the
// property that matters — "the client received it" is.
func hubServer(t *testing.T, opts ws.Opts) (*ws.Hub, string) {
	t.Helper()

	hub := ws.NewHub(nil, opts)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		account := r.URL.Query().Get("account")
		if err := hub.Upgrade(w, r, account); err != nil {
			t.Logf("upgrade: %v", err)
		}
	}))
	t.Cleanup(func() {
		hub.Close()
		srv.Close()
	})

	return hub, "ws" + strings.TrimPrefix(srv.URL, "http")
}

func dial(t *testing.T, url string) *websocket.Conn {
	t.Helper()

	c, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dialing %s: %v", url, err)
	}
	t.Cleanup(func() { _ = c.Close() })

	return c
}

// readEvent reads one frame and decodes it, failing the test on timeout —
// a push that never arrives is the failure this whole package exists to
// prevent.
func readEvent(t *testing.T, c *websocket.Conn) ws.Event {
	t.Helper()

	if err := c.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("read deadline: %v", err)
	}
	_, payload, err := c.ReadMessage()
	if err != nil {
		t.Fatalf("reading event: %v", err)
	}

	var e ws.Event
	if err := json.Unmarshal(payload, &e); err != nil {
		t.Fatalf("decoding event %q: %v", payload, err)
	}

	return e
}

// waitForCount polls the registry: registration happens on the server side
// of the handshake, so a freshly dialled client can observe it a hair early.
func waitForCount(t *testing.T, hub *ws.Hub, accountID string, want int) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if hub.Count(accountID) == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("connections for %s = %d, want %d", accountID, hub.Count(accountID), want)
}

// TestSendFansOutToEveryConnection: one account, several devices/tabs — the
// event reaches all of them, and nobody else's socket.
func TestSendFansOutToEveryConnection(t *testing.T) {
	hub, url := hubServer(t, ws.Opts{})

	first := dial(t, url+"?account=agent-1")
	second := dial(t, url+"?account=agent-1")
	other := dial(t, url+"?account=agent-2")
	waitForCount(t, hub, "agent-1", 2)
	waitForCount(t, hub, "agent-2", 1)

	sent := hub.Send("agent-1", ws.Event{
		Type: "transfer.offer_received",
		Data: map[string]string{"offer_id": "abc"},
	})
	if sent != 2 {
		t.Fatalf("deliveries = %d, want 2", sent)
	}

	for i, c := range []*websocket.Conn{first, second} {
		got := readEvent(t, c)
		if got.Type != "transfer.offer_received" || got.Data["offer_id"] != "abc" {
			t.Fatalf("connection %d received %+v", i, got)
		}
	}

	// The other account's socket must stay silent.
	if err := other.SetReadDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
		t.Fatalf("read deadline: %v", err)
	}
	if _, _, err := other.ReadMessage(); err == nil {
		t.Fatal("agent-2 received an event addressed to agent-1")
	}
}

// TestSendToUnknownAccount: an offline account is the normal case, not an
// error — nothing to deliver to, nothing to blow up.
func TestSendToUnknownAccount(t *testing.T) {
	hub, _ := hubServer(t, ws.Opts{})

	if sent := hub.Send("nobody-home", ws.Event{Type: "transfer.matched"}); sent != 0 {
		t.Fatalf("deliveries to an offline account = %d, want 0", sent)
	}
}

// TestClosedConnectionIsPruned: the registry follows the wire — a client
// that hangs up stops being a delivery target.
func TestClosedConnectionIsPruned(t *testing.T) {
	hub, url := hubServer(t, ws.Opts{})

	c := dial(t, url+"?account=agent-3")
	waitForCount(t, hub, "agent-3", 1)

	if err := c.Close(); err != nil {
		t.Fatalf("closing client: %v", err)
	}
	waitForCount(t, hub, "agent-3", 0)

	if sent := hub.Send("agent-3", ws.Event{Type: "transfer.matched"}); sent != 0 {
		t.Fatalf("deliveries after close = %d, want 0", sent)
	}
}

// TestUpgradeRequiresAccount: the hub refuses to register an anonymous
// socket, so a caller that forgets to authenticate cannot open one.
func TestUpgradeRequiresAccount(t *testing.T) {
	_, url := hubServer(t, ws.Opts{})

	if _, _, err := websocket.DefaultDialer.Dial(url+"?account=", nil); err == nil {
		t.Fatal("upgrade without an account id succeeded")
	}
}

// TestOriginIsChecked: a browser origin outside the allow-list never gets
// the protocol switch.
func TestOriginIsChecked(t *testing.T) {
	_, url := hubServer(t, ws.Opts{AllowedOrigins: []string{"https://app.mokapay.test"}})

	if _, _, err := websocket.DefaultDialer.Dial(url+"?account=agent-4",
		http.Header{"Origin": []string{"https://evil.test"}}); err == nil {
		t.Fatal("upgrade from a disallowed origin succeeded")
	}

	allowed := websocket.DefaultDialer
	c, _, err := allowed.Dial(url+"?account=agent-4",
		http.Header{"Origin": []string{"https://app.mokapay.test"}})
	if err != nil {
		t.Fatalf("upgrade from an allowed origin failed: %v", err)
	}
	_ = c.Close()
}

// TestDispatchRoutesEnvelopeToAddressee wires the two halves the way the
// gateway does: a bus payload in, that account's socket out.
func TestDispatchRoutesEnvelopeToAddressee(t *testing.T) {
	hub, url := hubServer(t, ws.Opts{})

	c := dial(t, url+"?account=agent-5")
	waitForCount(t, hub, "agent-5", 1)

	bus := &fakeBus{}
	if err := ws.NewPublisher(bus).Publish(context.Background(), "agent-5", ws.Event{
		Type: "transfer.offer_received",
		Data: map[string]string{"transfer_id": "t-1"},
	}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if bus.channel != ws.Channel {
		t.Fatalf("published on %q, want %q", bus.channel, ws.Channel)
	}

	delivered, err := hub.Dispatch(bus.payload)
	if err != nil || delivered != 1 {
		t.Fatalf("Dispatch = %d, %v", delivered, err)
	}

	got := readEvent(t, c)
	if got.Type != "transfer.offer_received" || got.Data["transfer_id"] != "t-1" {
		t.Fatalf("received %+v", got)
	}
}

// TestDispatchRejectsMalformedPayloads: garbage on the bus is reported, not
// swallowed, and never mistaken for a delivery.
func TestDispatchRejectsMalformedPayloads(t *testing.T) {
	hub, _ := hubServer(t, ws.Opts{})

	for name, payload := range map[string]string{
		"not json":         "}{",
		"no account":       `{"event_type":"transfer.matched"}`,
		"no event type":    `{"account_id":"agent-6"}`,
		"empty object":     `{}`,
		"account is empty": `{"account_id":"","event_type":"transfer.matched"}`,
	} {
		if _, err := hub.Dispatch(payload); err == nil {
			t.Fatalf("%s: Dispatch accepted %q", name, payload)
		}
	}
}

// TestPublisherWithoutBus: no event bus configured means the publish is a
// no-op, never an error the caller has to handle.
func TestPublisherWithoutBus(t *testing.T) {
	if err := ws.NewPublisher(nil).Publish(context.Background(), "agent-7", ws.Event{Type: "transfer.matched"}); err != nil {
		t.Fatalf("Publish without a bus = %v, want nil", err)
	}
}

// fakeBus captures what a publisher hands to redis.
type fakeBus struct {
	channel string
	payload string
}

func (b *fakeBus) Publish(_ context.Context, channel, payload string) error {
	b.channel = channel
	b.payload = payload
	return nil
}
