package live_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
	"google.golang.org/grpc"
	"mokapay.app/app/services/gateway/internal/api/live"
	"mokapay.app/core/client"
	coreweb "mokapay.app/core/http"
	"mokapay.app/core/logger"
	"mokapay.app/core/ws"
	userv1 "mokapay.app/proto/codebase/user/v1"
)

// fakeValidator stands in for cognito: the handshake must go through the
// very same AuthValidator the REST middleware uses, so a test that fails it
// is a test of the real gate.
type fakeValidator struct {
	claims jwt.RegisteredClaims
	err    error
}

func (v fakeValidator) ValidateToken(token string) (jwt.RegisteredClaims, error) {
	if v.err != nil {
		return jwt.RegisteredClaims{}, v.err
	}
	if token == "" {
		return jwt.RegisteredClaims{}, errors.New("empty token")
	}
	return v.claims, nil
}

// fakeUsers answers the account rehydration the handshake does after the
// token checks out. The embedded interface is nil on purpose: any other RPC
// would panic, which is the point — the handshake must call exactly this one.
type fakeUsers struct {
	userv1.UserServiceClient
	account *userv1.Account
	err     error
}

func (f fakeUsers) GetAccountBySub(_ context.Context, _ *userv1.GetAccountBySubRequest, _ ...grpc.CallOption) (*userv1.Account, error) {
	return f.account, f.err
}

func newServer(t *testing.T, v fakeValidator, users fakeUsers) (*ws.Hub, string) {
	t.Helper()

	log := logger.New(io.Discard, logger.LevelError, "test", nil)
	hub := ws.NewHub(nil, ws.Opts{})
	moka := &client.MokaClient{User: &client.User{User: users}}

	srv := coreweb.NewServer(log.Info, nil)
	live.NewHandler(log, hub, v, moka, nil).Routes(srv)

	ts := httptest.NewServer(srv)
	t.Cleanup(func() {
		hub.Close()
		ts.Close()
	})

	return hub, "ws" + strings.TrimPrefix(ts.URL, "http")
}

func account(id string) *userv1.Account {
	return &userv1.Account{Id: id, Role: userv1.AccountRole_ACCOUNT_ROLE_AGENT}
}

// TestHandshakeRejectsBadTokens: every way of failing auth is answered with
// a plain 401 BEFORE the protocol switch — an unauthenticated socket is not
// acceptable just because it is a new transport.
func TestHandshakeRejectsBadTokens(t *testing.T) {
	valid := fakeValidator{claims: jwt.RegisteredClaims{Subject: "sub-1"}}
	users := fakeUsers{account: account("account-1")}

	tests := map[string]struct {
		validator fakeValidator
		users     fakeUsers
		query     string
	}{
		"missing token": {
			validator: valid, users: users, query: "",
		},
		"empty token": {
			validator: valid, users: users, query: "?token=",
		},
		"invalid token": {
			validator: fakeValidator{err: errors.New("signature is invalid")},
			users:     users, query: "?token=garbage",
		},
		"expired token": {
			validator: fakeValidator{err: jwt.ErrTokenExpired},
			users:     users, query: "?token=expired",
		},
		"unknown account": {
			validator: valid,
			users:     fakeUsers{err: errors.New("account not found")},
			query:     "?token=orphan",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			hub, url := newServer(t, tc.validator, tc.users)

			conn, res, err := websocket.DefaultDialer.Dial(url+"/v1/ws"+tc.query, nil)
			if err == nil {
				_ = conn.Close()
				t.Fatal("handshake succeeded without valid credentials")
			}
			if res == nil || res.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status = %v, want 401", res)
			}
			if hub.Count("account-1") != 0 {
				t.Fatal("a rejected handshake registered a connection")
			}
		})
	}
}

// TestHandshakeAcceptsQueryToken: the browser path. A WebSocket constructor
// cannot set an Authorization header, so the token rides the query string —
// and the socket that comes out is registered to the token's account.
func TestHandshakeAcceptsQueryToken(t *testing.T) {
	hub, url := newServer(t,
		fakeValidator{claims: jwt.RegisteredClaims{Subject: "sub-1"}},
		fakeUsers{account: account("account-1")},
	)

	conn, _, err := websocket.DefaultDialer.Dial(url+"/v1/ws?token=good", nil)
	if err != nil {
		t.Fatalf("handshake with a valid token failed: %v", err)
	}
	defer conn.Close() //nolint:errcheck // test client

	waitForCount(t, hub, "account-1", 1)

	// The socket is live: an event addressed to that account arrives on it.
	if sent := hub.Send("account-1", ws.Event{Type: "transfer.offer_received"}); sent != 1 {
		t.Fatalf("deliveries = %d, want 1", sent)
	}
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("read deadline: %v", err)
	}
	if _, _, err := conn.ReadMessage(); err != nil {
		t.Fatalf("reading pushed event: %v", err)
	}
}

// TestHandshakeAcceptsBearerHeader: native clients (and this repo's smoke
// tests) can set the header — same validation, no query string.
func TestHandshakeAcceptsBearerHeader(t *testing.T) {
	hub, url := newServer(t,
		fakeValidator{claims: jwt.RegisteredClaims{Subject: "sub-2"}},
		fakeUsers{account: account("account-2")},
	)

	conn, _, err := websocket.DefaultDialer.Dial(url+"/v1/ws",
		http.Header{"Authorization": []string{"Bearer good"}})
	if err != nil {
		t.Fatalf("handshake with a bearer header failed: %v", err)
	}
	defer conn.Close() //nolint:errcheck // test client

	waitForCount(t, hub, "account-2", 1)
}

// TestHandshakeRejectsMalformedAuthorizationHeader: a header that is present
// but not a bearer token never falls back to the query param.
func TestHandshakeRejectsMalformedAuthorizationHeader(t *testing.T) {
	_, url := newServer(t,
		fakeValidator{claims: jwt.RegisteredClaims{Subject: "sub-3"}},
		fakeUsers{account: account("account-3")},
	)

	conn, res, err := websocket.DefaultDialer.Dial(url+"/v1/ws?token=good",
		http.Header{"Authorization": []string{"Basic aGk6dGhlcmU="}})
	if err == nil {
		_ = conn.Close()
		t.Fatal("handshake succeeded with a non-bearer Authorization header")
	}
	if res == nil || res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %v, want 401", res)
	}
}

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
