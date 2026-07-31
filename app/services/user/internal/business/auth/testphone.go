package auth

// The fixed phone numbers of the seeded test accounts (see internal.Seed).
// They are provisioned in every environment — local, CI, the shared dev
// server — so the team always has a working login without provisioning a
// user by hand. Two flows key off them:
//
//   - OTP generation keeps them on TestOtpCode instead of a random code.
//   - SMS delivery routes them to the logging sender, never to the real
//     provider — a real send would cost money to deliver a code the team
//     already knows.
//
// They live here rather than next to Seed because both the auth usecase and
// the service composition layer need them, and the business layer cannot
// import upwards.
const (
	TestPhoneAdmin = "+243812345670"
	TestPhoneUser  = "+243812345673"
	TestPhoneAgent = "+243812345675"
)

// TestOtpCode is the code every test phone above always receives. Fixed on
// purpose: it makes register/login/reset testable without an SMS round-trip.
const TestOtpCode = "111111"

// testPhones is the lookup set behind IsTestPhone.
var testPhones = map[string]struct{}{
	TestPhoneAdmin: {},
	TestPhoneUser:  {},
	TestPhoneAgent: {},
}

// IsTestPhone reports whether phoneE164 is one of the seeded test accounts.
// The argument must already be normalized to E.164 — the constants above are
// stored in that form.
func IsTestPhone(phoneE164 string) bool {
	_, ok := testPhones[phoneE164]
	return ok
}
