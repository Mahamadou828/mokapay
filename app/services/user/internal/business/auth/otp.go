package auth

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrOtpNotFound is returned when no live (unconsumed, unexpired) code
// matches — including losing the consume race to a parallel verify.
var ErrOtpNotFound = errors.New("otp not found")
var ErrInvalidCode = errors.New("invalid code")
var ErrInvalidPhoneNumber = errors.New("invalid phone number")
var ErrTooManyAttempts = errors.New("too many attempts")

// OtpPurpose values mirror the stored otp_purpose enum.
type OtpPurpose string

const (
	OtpPurposeRegister OtpPurpose = "register"
	OtpPurposeLogin    OtpPurpose = "login"
	OtpPurposeReset    OtpPurpose = "reset"
)

// Otp is the read entity for a one-time code. The plaintext code never
// exists outside the send path — only its bcrypt hash is stored.
type Otp struct {
	ID          uuid.UUID
	PhoneNumber string
	CodeHash    string
	Purpose     OtpPurpose
	Attempts    int
	ExpiresAt   time.Time
	ConsumedAt  *time.Time // nil while the code is live
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// NewOtp carries everything needed to store a freshly issued code.
type NewOtp struct {
	PhoneNumber string
	CodeHash    string
	Purpose     OtpPurpose
	ExpiresAt   time.Time
}
