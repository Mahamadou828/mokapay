package sms

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"mokapay.app/app/services/user/internal/business/auth"
)

// recorder is a Sender that only remembers what it was asked to deliver.
type recorder struct {
	name  string
	sends []string // "phone|message"
	err   error
}

func (r *recorder) Send(_ context.Context, phoneNumber, message string) error {
	r.sends = append(r.sends, phoneNumber+"|"+message)
	return r.err
}

func TestRouterSplitsTestPhonesFromLiveTraffic(t *testing.T) {
	live, test := &recorder{name: "live"}, &recorder{name: "test"}
	r := NewRouter(live, test, auth.IsTestPhone)
	ctx := context.Background()

	for _, phone := range []string{auth.TestPhoneAdmin, auth.TestPhoneUser, auth.TestPhoneAgent} {
		if err := r.Send(ctx, phone, auth.TestOtpCode); err != nil {
			t.Fatalf("%s: send: %v", phone, err)
		}
	}
	if err := r.Send(ctx, "+243812345671", "424242"); err != nil {
		t.Fatalf("real phone send: %v", err)
	}

	if len(live.sends) != 1 || live.sends[0] != "+243812345671|424242" {
		t.Fatalf("live sender got %v, want only the real phone", live.sends)
	}
	if len(test.sends) != 3 {
		t.Fatalf("test sender got %v, want the 3 seeded phones", test.sends)
	}
}

func TestRouterPropagatesSenderError(t *testing.T) {
	wantErr := errors.New("boom")
	live, test := &recorder{err: wantErr}, &recorder{}
	r := NewRouter(live, test, auth.IsTestPhone)

	if err := r.Send(context.Background(), "+243812345671", "424242"); !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
}

// The point of the whole arrangement: a seeded test phone must not reach the
// provider's HTTP endpoint, so no credit is spent and no code is texted to a
// number that may not be a real handset.
func TestRouterNeverCallsProviderForTestPhones(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"SMSMessageData":{"Message":"Sent to 1/1","Recipients":[{"number":"+243812345671","status":"Success","statusCode":101}]}}`))
	}))
	defer srv.Close()

	live := NewAfricasTalking(newTestLogger(), "moka", "key", "MOKA")
	live.url = srv.URL

	logged := &recorder{}
	r := NewRouter(live, logged, auth.IsTestPhone)
	ctx := context.Background()

	for _, phone := range []string{auth.TestPhoneAdmin, auth.TestPhoneUser, auth.TestPhoneAgent} {
		if err := r.Send(ctx, phone, auth.TestOtpCode); err != nil {
			t.Fatalf("%s: send: %v", phone, err)
		}
	}
	if calls != 0 {
		t.Fatalf("provider was called %d time(s) for test phones, want 0", calls)
	}
	if len(logged.sends) != 3 {
		t.Fatalf("test sender got %v, want the 3 seeded phones", logged.sends)
	}

	// …while a real number does go out over HTTP.
	if err := r.Send(ctx, "+243812345671", "424242"); err != nil {
		t.Fatalf("real phone send: %v", err)
	}
	if calls != 1 {
		t.Fatalf("provider was called %d time(s) for a real phone, want 1", calls)
	}
}
