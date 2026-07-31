package sms

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"

	"mokapay.app/core/logger"
)

func newTestLogger() *logger.Logger {
	return logger.New(os.Stderr, logger.LevelInfo, "test", nil)
}

// atServer stands in for the Messaging API: it captures the request and
// replies with the canned body/status the test asks for.
type atServer struct {
	*httptest.Server
	method string
	path   string
	apiKey string
	accept string
	ctype  string
	form   url.Values
}

func newATServer(t *testing.T, status int, body string) *atServer {
	t.Helper()

	s := &atServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading request body: %v", err)
			return
		}
		form, err := url.ParseQuery(string(raw))
		if err != nil {
			t.Errorf("parsing form body: %v", err)
			return
		}

		s.method, s.path, s.form = r.Method, r.URL.Path, form
		s.apiKey = r.Header.Get("apiKey")
		s.accept = r.Header.Get("Accept")
		s.ctype = r.Header.Get("Content-Type")

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if _, err := w.Write([]byte(body)); err != nil {
			t.Errorf("writing response: %v", err)
		}
	}))
	t.Cleanup(s.Close)

	return s
}

// newATSender points the live sender at the stub instead of the real API — no
// unit test ever touches Africa's Talking.
func newATSender(srvURL, username, apiKey, senderID string) AfricasTalking {
	s := NewAfricasTalking(newTestLogger(), username, apiKey, senderID)
	s.url = srvURL
	return s
}

func TestAfricasTalkingSendRequestShape(t *testing.T) {
	srv := newATServer(t, http.StatusCreated,
		`{"SMSMessageData":{"Message":"Sent to 1/1 Total Cost: USD 0.0100","Recipients":[{"number":"+243812345671","status":"Success","statusCode":101,"cost":"USD 0.0100","messageId":"ATXid_1"}]}}`)

	s := newATSender(srv.URL, "mokapay", "secret-key", "MOKA")
	if err := s.Send(context.Background(), "+243812345671", "424242"); err != nil {
		t.Fatalf("send: %v", err)
	}

	if srv.method != http.MethodPost {
		t.Errorf("method = %s, want POST", srv.method)
	}
	if srv.apiKey != "secret-key" {
		t.Errorf("apiKey header = %q, want the configured key", srv.apiKey)
	}
	if srv.accept != "application/json" {
		t.Errorf("Accept = %q, want application/json", srv.accept)
	}
	if srv.ctype != "application/x-www-form-urlencoded" {
		t.Errorf("Content-Type = %q, want application/x-www-form-urlencoded", srv.ctype)
	}

	for field, want := range map[string]string{
		"username":    "mokapay",
		"to":          "+243812345671",
		"message":     "424242",
		"bulkSMSMode": "1",
		"from":        "MOKA",
	} {
		if got := srv.form.Get(field); got != want {
			t.Errorf("form %q = %q, want %q", field, got, want)
		}
	}
}

// An empty sender id means "use their shared pool" — the field must be absent
// rather than sent empty, which AT rejects as an invalid sender.
func TestAfricasTalkingSendOmitsEmptySenderID(t *testing.T) {
	srv := newATServer(t, http.StatusCreated,
		`{"SMSMessageData":{"Message":"Sent to 1/1","Recipients":[{"number":"+243812345671","status":"Success","statusCode":101}]}}`)

	s := newATSender(srv.URL, "mokapay", "secret-key", "")
	if err := s.Send(context.Background(), "+243812345671", "424242"); err != nil {
		t.Fatalf("send: %v", err)
	}

	if _, ok := srv.form["from"]; ok {
		t.Errorf("form carries from=%q, want the field omitted", srv.form.Get("from"))
	}
}

// Accepted recipient statuses: 100 Processed, 101 Sent, 102 Queued.
func TestAfricasTalkingSendAcceptsQueuedStatuses(t *testing.T) {
	for _, code := range []string{"100", "101", "102"} {
		srv := newATServer(t, http.StatusCreated,
			`{"SMSMessageData":{"Message":"Sent to 1/1","Recipients":[{"number":"+243812345671","status":"Success","statusCode":`+code+`}]}}`)

		s := newATSender(srv.URL, "mokapay", "secret-key", "MOKA")
		if err := s.Send(context.Background(), "+243812345671", "424242"); err != nil {
			t.Errorf("statusCode %s: send = %v, want nil", code, err)
		}
	}
}

// A 2xx carrying a rejected recipient is still a failed delivery.
func TestAfricasTalkingSendRejectedRecipient(t *testing.T) {
	srv := newATServer(t, http.StatusCreated,
		`{"SMSMessageData":{"Message":"Sent to 0/1","Recipients":[{"number":"+243812345671","status":"InvalidPhoneNumber","statusCode":403}]}}`)

	s := newATSender(srv.URL, "mokapay", "secret-key", "MOKA")
	err := s.Send(context.Background(), "+243812345671", "424242")
	if !errors.Is(err, ErrSendFailed) {
		t.Fatalf("err = %v, want ErrSendFailed", err)
	}
}

// A 2xx with no recipient at all: the request itself was refused.
func TestAfricasTalkingSendEmptyRecipients(t *testing.T) {
	srv := newATServer(t, http.StatusCreated,
		`{"SMSMessageData":{"Message":"Invalid sender id","Recipients":[]}}`)

	s := newATSender(srv.URL, "mokapay", "secret-key", "BAD")
	err := s.Send(context.Background(), "+243812345671", "424242")
	if !errors.Is(err, ErrSendFailed) {
		t.Fatalf("err = %v, want ErrSendFailed", err)
	}
}

func TestAfricasTalkingSendHTTPError(t *testing.T) {
	srv := newATServer(t, http.StatusUnauthorized, `Unauthorized`)

	s := newATSender(srv.URL, "mokapay", "wrong-key", "MOKA")
	err := s.Send(context.Background(), "+243812345671", "424242")
	if !errors.Is(err, ErrSendFailed) {
		t.Fatalf("err = %v, want ErrSendFailed", err)
	}
}

func TestAfricasTalkingSendTransportError(t *testing.T) {
	srv := newATServer(t, http.StatusCreated, `{}`)
	srv.Close() // nothing listening: the call must fail, not silently pass

	s := newATSender(srv.URL, "mokapay", "secret-key", "MOKA")
	if err := s.Send(context.Background(), "+243812345671", "424242"); err == nil {
		t.Fatal("send to a dead endpoint returned nil")
	}
}

// The sandbox is selected by username, not by a flag.
func TestNewAfricasTalkingPicksEndpointByUsername(t *testing.T) {
	if got := NewAfricasTalking(newTestLogger(), "mokapay", "k", "").url; got != atProductionURL {
		t.Errorf("url = %q, want %q", got, atProductionURL)
	}
	if got := NewAfricasTalking(newTestLogger(), atSandboxUser, "k", "").url; got != atSandboxURL {
		t.Errorf("url = %q, want %q", got, atSandboxURL)
	}
}
