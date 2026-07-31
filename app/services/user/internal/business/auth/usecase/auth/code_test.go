package auth

import (
	"testing"

	"mokapay.app/app/services/user/internal/business/auth"
)

// newTestAuth builds the minimum Auth needed by getCodeAndHash: the hasher.
func newTestAuth(t *testing.T) Auth {
	t.Helper()

	h, err := NewHasher([]byte("test-pepper"))
	if err != nil {
		t.Fatalf("building hasher: %v", err)
	}
	return Auth{hasher: h}
}

// The seeded test phones must keep the fixed code — that is what lets the
// team log into any environment without reading an SMS.
func TestGetCodeAndHashFixedForTestPhones(t *testing.T) {
	a := newTestAuth(t)

	for _, phone := range []string{auth.TestPhoneAdmin, auth.TestPhoneUser, auth.TestPhoneAgent} {
		// Repeat: a fixed code must not drift between sends/resends.
		for range 3 {
			code, hash, err := a.getCodeAndHash(phone)
			if err != nil {
				t.Fatalf("%s: getCodeAndHash: %v", phone, err)
			}
			if code != auth.TestOtpCode {
				t.Fatalf("%s: code = %q, want the fixed %q", phone, code, auth.TestOtpCode)
			}
			if !a.hasher.Verify(hash, phone, code) {
				t.Fatalf("%s: returned hash does not verify against the returned code", phone)
			}
		}
	}
}

// Every other number gets a real random code.
func TestGetCodeAndHashRandomForRealPhones(t *testing.T) {
	a := newTestAuth(t)
	const phone = "+243812345671" // not a seeded test account

	const draws = 25
	seen := make(map[string]struct{}, draws)
	fixed := 0

	for range draws {
		code, hash, err := a.getCodeAndHash(phone)
		if err != nil {
			t.Fatalf("getCodeAndHash: %v", err)
		}

		if len(code) != 6 {
			t.Fatalf("code = %q, want 6 digits", code)
		}
		for _, r := range code {
			if r < '0' || r > '9' {
				t.Fatalf("code = %q, want digits only", code)
			}
		}
		if !a.hasher.Verify(hash, phone, code) {
			t.Fatalf("code %q: returned hash does not verify", code)
		}
		if code == auth.TestOtpCode {
			fixed++
		}
		seen[code] = struct{}{}
	}

	// A real generator draws from 10^6, so 25 draws collapsing to one value
	// means the code is not random at all.
	if len(seen) < 2 {
		t.Fatalf("got %d distinct code(s) over %d draws (%v), want random codes", len(seen), draws, seen)
	}
	// 111111 is a legal random draw, but hitting it every time means the
	// hardcoded value is still in the path.
	if fixed == draws {
		t.Fatalf("every draw returned the fixed %q for a non-test phone", auth.TestOtpCode)
	}
}

// A test phone and a real phone must not share a code hash even when the code
// happens to match — the hash is peppered per phone.
func TestGetCodeAndHashIsPerPhone(t *testing.T) {
	a := newTestAuth(t)

	_, hash, err := a.getCodeAndHash(auth.TestPhoneUser)
	if err != nil {
		t.Fatalf("getCodeAndHash: %v", err)
	}
	if a.hasher.Verify(hash, "+243812345671", auth.TestOtpCode) {
		t.Fatal("a test phone's hash verified against another phone number")
	}
}
