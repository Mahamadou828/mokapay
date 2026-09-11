package sms

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

const (
	testTwilioSID   = "AC00000000000000000000000000000001"
	testTwilioToken = "twilio-secret"
)

// twilioServer stands in for the Messages API: it captures the request and
// replies with the canned body/status the test asks for.
type twilioServer struct {
	*httptest.Server
	method   string
	path     string
	user     string
	password string
	basicOK  bool
	accept   string
	ctype    string
	form     url.Values
}

func newTwilioServer(t *testing.T, status int, body string) *twilioServer {
	t.Helper()

	s := &twilioServer{}
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
		s.user, s.password, s.basicOK = r.BasicAuth()
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

// newTwilioSender points the live sender at the stub instead of the real API
// — no unit test ever touches Twilio. The stub keeps Twilio's real path so
// the request-shape test can assert on it.
func newTwilioSender(t *testing.T, srvURL, from, messagingServiceSID string) Twilio {
	t.Helper()

	s, err := NewTwilio(newTestLogger(), testTwilioSID, testTwilioToken, from, messagingServiceSID)
	if err != nil {
		t.Fatalf("new twilio: %v", err)
	}
	s.url = srvURL + "/2010-04-01/Accounts/" + testTwilioSID + "/Messages.json"
	return s
}

func TestTwilioSendRequestShape(t *testing.T) {
	srv := newTwilioServer(t, http.StatusCreated,
		`{"sid":"SM1","status":"queued","error_code":null,"error_message":null}`)

	s := newTwilioSender(t, srv.URL, "+15005550006", "")
	if err := s.Send(context.Background(), "+243812345671", "424242"); err != nil {
		t.Fatalf("send: %v", err)
	}

	if srv.method != http.MethodPost {
		t.Errorf("method = %s, want POST", srv.method)
	}
	if want := "/2010-04-01/Accounts/" + testTwilioSID + "/Messages.json"; srv.path != want {
		t.Errorf("path = %q, want %q", srv.path, want)
	}
	if !srv.basicOK {
		t.Fatal("request carried no basic auth")
	}
	if srv.user != testTwilioSID || srv.password != testTwilioToken {
		t.Errorf("basic auth = %q:%q, want the configured sid and token", srv.user, srv.password)
	}
	if srv.accept != "application/json" {
		t.Errorf("Accept = %q, want application/json", srv.accept)
	}
	if srv.ctype != "application/x-www-form-urlencoded" {
		t.Errorf("Content-Type = %q, want application/x-www-form-urlencoded", srv.ctype)
	}

	for field, want := range map[string]string{
		"To":   "+243812345671",
		"Body": "424242",
		"From": "+15005550006",
	} {
		if got := srv.form.Get(field); got != want {
			t.Errorf("form %q = %q, want %q", field, got, want)
		}
	}
}

// A messaging service replaces the sender number: the two are mutually
// exclusive on Twilio's side, so From must be absent entirely.
func TestTwilioSendUsesMessagingService(t *testing.T) {
	srv := newTwilioServer(t, http.StatusCreated,
		`{"sid":"SM2","status":"accepted","error_code":null}`)

	s := newTwilioSender(t, srv.URL, "", "MG00000000000000000000000000000002")
	if err := s.Send(context.Background(), "+243812345671", "424242"); err != nil {
		t.Fatalf("send: %v", err)
	}

	if got := srv.form.Get("MessagingServiceSid"); got != "MG00000000000000000000000000000002" {
		t.Errorf("form MessagingServiceSid = %q, want the configured sid", got)
	}
	if _, ok := srv.form["From"]; ok {
		t.Errorf("form carries From=%q, want the field omitted", srv.form.Get("From"))
	}
}

// Accepted message statuses: the message is on its way out.
func TestTwilioSendAcceptsQueuedStatuses(t *testing.T) {
	for _, status := range []string{"queued", "accepted", "sending", "sent", "scheduled"} {
		srv := newTwilioServer(t, http.StatusCreated,
			`{"sid":"SM3","status":"`+status+`","error_code":null}`)

		s := newTwilioSender(t, srv.URL, "+15005550006", "")
		if err := s.Send(context.Background(), "+243812345671", "424242"); err != nil {
			t.Errorf("status %s: send = %v, want nil", status, err)
		}
	}
}

// A 2xx carrying a terminal failure status is still a failed delivery.
func TestTwilioSendRejectedStatus(t *testing.T) {
	for _, status := range []string{"failed", "undelivered"} {
		srv := newTwilioServer(t, http.StatusCreated,
			`{"sid":"SM4","status":"`+status+`","error_code":null,"error_message":"unreachable"}`)

		s := newTwilioSender(t, srv.URL, "+15005550006", "")
		err := s.Send(context.Background(), "+243812345671", "424242")
		if !errors.Is(err, ErrSendFailed) {
			t.Errorf("status %s: err = %v, want ErrSendFailed", status, err)
		}
	}
}

// An error code on the message resource means Twilio created it and refused
// to deliver, whatever the status says.
func TestTwilioSendErrorCodeOnMessage(t *testing.T) {
	srv := newTwilioServer(t, http.StatusCreated,
		`{"sid":"SM5","status":"queued","error_code":21610,"error_message":"unsubscribed recipient"}`)

	s := newTwilioSender(t, srv.URL, "+15005550006", "")
	err := s.Send(context.Background(), "+243812345671", "424242")
	if !errors.Is(err, ErrSendFailed) {
		t.Fatalf("err = %v, want ErrSendFailed", err)
	}
	if !strings.Contains(err.Error(), "21610") {
		t.Errorf("err = %v, want it to carry the twilio error code", err)
	}
}

func TestTwilioSendHTTPErrorWithTwilioBody(t *testing.T) {
	srv := newTwilioServer(t, http.StatusBadRequest,
		`{"code":21211,"message":"The 'To' number is not a valid phone number.","more_info":"https://www.twilio.com/docs/errors/21211","status":400}`)

	s := newTwilioSender(t, srv.URL, "+15005550006", "")
	err := s.Send(context.Background(), "not-a-number", "424242")
	if !errors.Is(err, ErrSendFailed) {
		t.Fatalf("err = %v, want ErrSendFailed", err)
	}
	if !strings.Contains(err.Error(), "21211") {
		t.Errorf("err = %v, want it to carry the twilio error code", err)
	}
}

// A non-JSON error body (a proxy in between, say) still has to surface as a
// send failure rather than a decode error.
func TestTwilioSendHTTPErrorWithRawBody(t *testing.T) {
	srv := newTwilioServer(t, http.StatusUnauthorized, `Unauthorized`)

	s := newTwilioSender(t, srv.URL, "+15005550006", "")
	err := s.Send(context.Background(), "+243812345671", "424242")
	if !errors.Is(err, ErrSendFailed) {
		t.Fatalf("err = %v, want ErrSendFailed", err)
	}
}

func TestTwilioSendTransportError(t *testing.T) {
	srv := newTwilioServer(t, http.StatusCreated, `{}`)
	srv.Close() // nothing listening: the call must fail, not silently pass

	s := newTwilioSender(t, srv.URL, "+15005550006", "")
	if err := s.Send(context.Background(), "+243812345671", "424242"); err == nil {
		t.Fatal("send to a dead endpoint returned nil")
	}
}

// Credentials and exactly one sender are wiring requirements: a mis-wire has
// to fail at startup, not on the first real OTP.
func TestNewTwilioRejectsBadWiring(t *testing.T) {
	tests := map[string]struct {
		accountSID          string
		authToken           string
		from                string
		messagingServiceSID string
	}{
		"no account sid":     {"", testTwilioToken, "+15005550006", ""},
		"no auth token":      {testTwilioSID, "", "+15005550006", ""},
		"no sender at all":   {testTwilioSID, testTwilioToken, "", ""},
		"both senders wired": {testTwilioSID, testTwilioToken, "+15005550006", "MG1"},
	}

	for name, tt := range tests {
		if _, err := NewTwilio(newTestLogger(), tt.accountSID, tt.authToken, tt.from, tt.messagingServiceSID); err == nil {
			t.Errorf("%s: NewTwilio returned nil error", name)
		}
	}
}

func TestNewTwilioBuildsAccountURL(t *testing.T) {
	s, err := NewTwilio(newTestLogger(), testTwilioSID, testTwilioToken, "+15005550006", "")
	if err != nil {
		t.Fatalf("new twilio: %v", err)
	}

	want := "https://api.twilio.com/2010-04-01/Accounts/" + testTwilioSID + "/Messages.json"
	if s.url != want {
		t.Errorf("url = %q, want %q", s.url, want)
	}
}
