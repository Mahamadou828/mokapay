package auth

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"time"

	"mokapay.app/app/services/user/internal/business/auth"
	"mokapay.app/app/services/user/internal/business/auth/repo"
	"mokapay.app/core/aws"
	coredb "mokapay.app/core/db"
	"mokapay.app/core/logger"
)

const codeSpace = 1_000_000 // 6 digits: 000000-999999
const maxAttempts = 6
const otpTTL = 5 * time.Minute

// otpMessage is the message body — the code alone, with no context, reads as
// spam/phishing to a recipient and to carrier filters alike. SMS and email
// share it on purpose: one wording, no second copy to drift out of sync.
const otpMessage = "Votre code MokaPay est %s"

// otpSubject is the email subject. The code itself stays out of it — subjects
// show up in notification previews on a locked screen.
const otpSubject = "Votre code MokaPay"

type OTPSender interface {
	Send(ctx context.Context, phoneNumber, msg string) error
}

// EmailSender delivers the OTP to an account that asked for the email method.
// Declared here, with OTPSender, because the usecase is the consumer — the
// integration package owns the implementations, not the contract.
type EmailSender interface {
	Send(ctx context.Context, email, subject, body string) error
}

type AuthProvider interface {
	SignUp(ctx context.Context, phone, pwd string) (sub string, err error)
	Login(ctx context.Context, phone, password string) (accessToken, refreshToken string, expiresIn time.Duration, err error)
	Refresh(ctx context.Context, refreshToken, sub string) (accessToken, newRefreshToken string, expiresIn time.Duration, err error)
	ResetPassword(ctx context.Context, phone, newPwd string) error
	Logout(ctx context.Context, sub string) error
	AdminDelete(ctx context.Context, username string) error
}

type Auth struct {
	logger   *logger.Logger
	repo     repo.Store
	db       *coredb.Postgres
	otp      OTPSender
	email    EmailSender
	provider AuthProvider
	hasher   Hasher
	limiter  Limiter
}

func New(l *logger.Logger, db *coredb.Postgres, otp OTPSender, email EmailSender, p AuthProvider, hash string, limiter Limiter) (Auth, error) {
	h, err := NewHasher([]byte(hash))
	if err != nil {
		return Auth{}, err
	}

	return Auth{
		repo:     repo.New(db),
		otp:      otp,
		email:    email,
		provider: p,
		logger:   l,
		db:       db,
		hasher:   h,
		limiter:  limiter,
	}, nil
}

// SendOTP issues (or resends) a code and returns its expiry, so the api layer
// can tell the client how long the code lives.
func (a Auth) SendOTP(ctx context.Context, phoneNumber string, purpose auth.OtpPurpose) (time.Time, error) {
	now := time.Now()
	expiresAt := now.Add(otpTTL)

	// check if phone number is correct
	phoneNumber, err := normalizePhoneNumber(phoneNumber)
	if err != nil {
		a.logger.Error(ctx, "Auth.SendOTP: failed to parse phone number", "err", err)
		return time.Time{}, auth.ErrInvalidPhoneNumber
	}

	// Rate limits before any work: an SMS costs money — cooldown + hourly
	// cap per phone (redis; fails open on an outage).
	if err := a.limiter.AllowSend(ctx, phoneNumber); err != nil {
		return time.Time{}, err
	}

	// check if the user already has such otp open
	otp, err := a.repo.GetValidOtp(ctx, phoneNumber, purpose)
	if err != nil {
		if !errors.Is(err, auth.ErrOtpNotFound) {
			a.logger.Error(ctx, "Auth.SendOTP: failed to get valid otp", "err", err)
			return time.Time{}, err
		}
	}

	// Previous otp found
	if err == nil {
		// too many attempt
		if otp.Attempts > maxAttempts {
			return time.Time{}, auth.ErrTooManyAttempts
		}

		// resend a new code
		code, hashCode, err := a.getCodeAndHash(phoneNumber)
		if err != nil {
			a.logger.Error(ctx, "Auth.SendOTP: failed to get code", "err", err)
			return time.Time{}, err
		}
		// send otp
		if err := a.deliverOTP(ctx, phoneNumber, code, purpose); err != nil {
			a.logger.Error(ctx, "Auth.SendOTP: failed to send otp request", "err", err)
			return time.Time{}, err
		}

		// update code hash in otp and attempt
		if _, err := a.repo.RefreshOtp(ctx, otp.ID.String(), hashCode, expiresAt); err != nil {
			a.logger.Error(ctx, "Auth.SendOTP: failed to refresh otp", "err", err)
			return time.Time{}, err
		}

		return expiresAt, nil
	}

	code, hashCode, err := a.getCodeAndHash(phoneNumber)
	if err != nil {
		a.logger.Error(ctx, "Auth.SendOTP: failed to get code", "err", err)
		return time.Time{}, fmt.Errorf("otp: generate: %w", err)
	}

	// send otp
	if err := a.deliverOTP(ctx, phoneNumber, code, purpose); err != nil {
		a.logger.Error(ctx, "Auth.SendOTP: failed to send otp request", "err", err)
		return time.Time{}, err
	}

	// create otp attempt
	if _, err := a.repo.AddOtp(ctx, auth.NewOtp{
		PhoneNumber: phoneNumber,
		CodeHash:    hashCode,
		Purpose:     purpose,
		ExpiresAt:   expiresAt,
	}); err != nil {
		a.logger.Error(ctx, "Auth.SendOTP: failed to create otp attempt", "err", err)
		return time.Time{}, err
	}

	return expiresAt, nil
}

// deliverOTP sends code to the account's chosen channel: email when it asked
// for it, SMS otherwise. phoneNumber must already be in E.164 form.
func (a Auth) deliverOTP(ctx context.Context, phoneNumber, code string, purpose auth.OtpPurpose) error {
	if to := a.otpEmailRecipient(ctx, phoneNumber, purpose); to != "" {
		return a.email.Send(ctx, to, otpSubject, fmt.Sprintf(otpMessage, code))
	}

	return a.otp.Send(ctx, phoneNumber, fmt.Sprintf(otpMessage, code))
}

// otpEmailRecipient returns the address to email the code to, or "" to send
// it by SMS. Every uncertainty resolves to SMS: registration has no account
// row to hold a preference, the seeded test phones keep their logging SMS
// round-trip (their code is public, an email adds nothing), and an account
// that asked for email but has none on file degrades rather than losing OTP
// delivery entirely — a data gap must never lock a user out. A lookup
// failure is logged and degrades the same way.
func (a Auth) otpEmailRecipient(ctx context.Context, phoneNumber string, purpose auth.OtpPurpose) string {
	if purpose == auth.OtpPurposeRegister || auth.IsTestPhone(phoneNumber) {
		return ""
	}

	account, err := a.repo.GetAccountByPhone(ctx, phoneNumber)
	if err != nil {
		// Not found is expected: SendOTP is pre-auth and does not assert the
		// account exists. Anything else is worth a line.
		if !errors.Is(err, auth.ErrAccountNotFound) {
			a.logger.Error(ctx, "Auth.otpEmailRecipient: failed to get account, falling back to sms", "err", err)
		}
		return ""
	}

	if account.PreferredOtpMethod != auth.OtpMethodEmail {
		return ""
	}
	if account.Email == nil || *account.Email == "" {
		a.logger.Info(ctx, "Auth.otpEmailRecipient: account prefers email but has none on file, falling back to sms", "account_id", account.ID)
		return ""
	}

	return *account.Email
}

func (a Auth) RegisterUser(ctx context.Context, na auth.NewAccount) (auth.Token, error) {
	now := time.Now()

	// check otp for phone number validation
	phoneNumber, err := normalizePhoneNumber(na.PhoneNumber)
	if err != nil {
		a.logger.Error(ctx, "Auth.RegisterUser: failed to parse phone number", "err", err)
		return auth.Token{}, auth.ErrInvalidPhoneNumber
	}

	// verify the register otp; consumed on success
	if err := a.CheckAndConsumeOTP(ctx, phoneNumber, na.Code, auth.OtpPurposeRegister); err != nil {
		return auth.Token{}, err
	}

	// create user account in auth provider
	sub, err := a.provider.SignUp(ctx, phoneNumber, na.Password)
	if err != nil {
		a.logger.Error(ctx, "Auth.RegisterUser: failed to sign up user", "err", err)
		return auth.Token{}, fmt.Errorf("failed to sign up user: %w", err)
	}

	// create user account. On failure, compensate by deleting the Cognito
	// user — an orphan there would block this phone from ever registering.
	na.PhoneNumber = phoneNumber
	if _, err := a.repo.AddAccount(ctx, na, sub); err != nil {
		a.logger.Error(ctx, "Auth.RegisterUser: failed to add account", "err", err)
		if delErr := a.provider.AdminDelete(ctx, phoneNumber); delErr != nil {
			a.logger.Error(ctx, "Auth.RegisterUser: failed to delete cognito user after add account failure — orphan user, needs manual cleanup", "err", delErr, "phone", phoneNumber)
		}
		return auth.Token{}, fmt.Errorf("failed to add account: %w", err)
	}

	// get tokens
	accessToken, refreshToken, expiresIn, err := a.provider.Login(ctx, phoneNumber, na.Password)
	if err != nil {
		a.logger.Error(ctx, "Auth.RegisterUser: failed to login user", "err", err)
		return auth.Token{}, fmt.Errorf("failed to login user: %w", err)
	}

	return auth.Token{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresAt:    now.Add(expiresIn),
	}, nil
}

func (a Auth) Login(ctx context.Context, phoneNumber, password, code string) (auth.Token, error) {
	now := time.Now()

	// check otp for phone number validation
	phoneNumber, err := normalizePhoneNumber(phoneNumber)
	if err != nil {
		a.logger.Error(ctx, "Auth.Login: failed to parse phone number", "err", err)
		return auth.Token{}, auth.ErrInvalidPhoneNumber
	}

	// the account must exist and be in a state allowed to authenticate —
	// checked before the otp so a blocked user doesn't burn a code
	account, err := a.repo.GetAccountByPhone(ctx, phoneNumber)
	if err != nil {
		a.logger.Error(ctx, "Auth.Login: failed to get account", "err", err)
		return auth.Token{}, err
	}
	if err := statusCanAuth(account.Status); err != nil {
		return auth.Token{}, err
	}

	// verify the login otp (2fa); consumed on success
	if err := a.CheckAndConsumeOTP(ctx, phoneNumber, code, auth.OtpPurposeLogin); err != nil {
		return auth.Token{}, err
	}

	// login user from provider
	accessToken, refreshToken, expiresIn, err := a.provider.Login(ctx, phoneNumber, password)
	if err != nil {
		a.logger.Error(ctx, "Auth.Login: failed to login user", "err", err)
		return auth.Token{}, fmt.Errorf("failed to login user: %w", err)
	}

	// stamp the login; non-fatal — the login itself already succeeded
	if _, err := a.repo.SetLastLogin(ctx, account.ID.String(), now); err != nil {
		a.logger.Error(ctx, "Auth.Login: failed to set last login", "err", err)
	}

	return auth.Token{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresAt:    now.Add(expiresIn),
	}, err

}
func (a Auth) RefreshToken(ctx context.Context, refreshToken, sub string) (auth.Token, error) {
	now := time.Now()

	// re-check the account: a suspended/banned user must not mint new tokens
	account, err := a.repo.GetAccountBySub(ctx, sub)
	if err != nil {
		a.logger.Error(ctx, "Auth.RefreshToken: failed to get account", "err", err)
		return auth.Token{}, err
	}
	if err := statusCanAuth(account.Status); err != nil {
		return auth.Token{}, err
	}

	// refresh token from provider
	accessToken, refreshToken, expiresIn, err := a.provider.Refresh(ctx, refreshToken, sub)
	if err != nil {
		a.logger.Error(ctx, "Auth.RefreshToken: failed to refresh token", "err", err)
		return auth.Token{}, fmt.Errorf("failed to refresh token: %w", err)
	}

	return auth.Token{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresAt:    now.Add(expiresIn),
	}, err
}

func (a Auth) ChangePassword(ctx context.Context, phoneNumber, code, newPwd string) error {
	// the otp was stored under the E.164 form — normalize before looking it up
	phoneNumber, err := normalizePhoneNumber(phoneNumber)
	if err != nil {
		a.logger.Error(ctx, "Auth.ChangePassword: failed to parse phone number", "err", err)
		return auth.ErrInvalidPhoneNumber
	}

	// verify the reset otp; consumed on success
	if err := a.CheckAndConsumeOTP(ctx, phoneNumber, code, auth.OtpPurposeReset); err != nil {
		return err
	}

	// reset password in provider
	if err := a.provider.ResetPassword(ctx, phoneNumber, newPwd); err != nil {
		a.logger.Error(ctx, "Auth.ChangePassword: failed to reset password", "err", err)
		if !errors.Is(err, aws.ErrFailToLogOut) {
			return fmt.Errorf("failed to reset password: %w", err)
		}
	}
	return nil
}

// LogOut revokes every session of the caller. The caller is identified by the
// Cognito sub from its token; Cognito's sign-out wants the username (phone).
func (a Auth) LogOut(ctx context.Context, sub string) error {
	account, err := a.repo.GetAccountBySub(ctx, sub)
	if err != nil {
		a.logger.Error(ctx, "Auth.LogOut: failed to get account", "err", err)
		return err
	}

	return a.provider.Logout(ctx, account.PhoneNumber)
}

// ValidateOTP verifies the code sent for phoneNumber/purpose WITHOUT burning
// it — the client calls it at the code-entry step so a mistyped code fails
// there instead of several screens later. The flow's terminal call still runs
// CheckAndConsumeOTP, so the code is checked twice on the happy path and
// consumed exactly once. phoneNumber must already be in E.164 form. A failed
// verify counts against the attempts cap, same as a consuming check.
func (a Auth) ValidateOTP(ctx context.Context, phoneNumber, code string, purpose auth.OtpPurpose) error {
	// Unlike CheckAndConsumeOTP this is called straight from the api layer,
	// so it normalizes like SendOTP does — codes are stored under E.164.
	phoneNumber, err := normalizePhoneNumber(phoneNumber)
	if err != nil {
		a.logger.Error(ctx, "Auth.ValidateOTP: failed to parse phone number", "err", err)
		return auth.ErrInvalidPhoneNumber
	}

	// Same rate limit as the consuming path: validate-spam is the guessing
	// attack AllowVerify exists for.
	if err := a.limiter.AllowVerify(ctx, phoneNumber); err != nil {
		return err
	}

	if _, err := a.checkOTP(ctx, phoneNumber, code, purpose); err != nil {
		return err
	}

	return nil
}

// CheckAndConsumeOTP verifies the code sent for phoneNumber/purpose and burns
// it on success. phoneNumber must already be in E.164 form — codes are stored
// under it. A failed verify counts against the attempts cap.
func (a Auth) CheckAndConsumeOTP(ctx context.Context, phoneNumber, code string, purpose auth.OtpPurpose) error {
	// The DB caps attempts per CODE (in checkOTP); this caps guesses per
	// PHONE across codes, so cycling fresh codes never resets the budget.
	if err := a.limiter.AllowVerify(ctx, phoneNumber); err != nil {
		return err
	}

	otp, err := a.checkOTP(ctx, phoneNumber, code, purpose)
	if err != nil {
		return err
	}

	if _, err := a.repo.ConsumeOtp(ctx, otp.ID.String()); err != nil {
		a.logger.Error(ctx, "Auth.CheckAndConsumeOTP: failed to consume otp attempt", "err", err)
		return fmt.Errorf("failed to consume otp attempt: %w", err)
	}

	return nil
}

// checkOTP resolves the live otp for phoneNumber/purpose and verifies code
// against it, returning the otp so the caller can consume it. A wrong code
// bumps the per-code attempts counter and yields auth.ErrInvalidCode; it never
// consumes. Callers apply the per-phone rate limit before calling.
func (a Auth) checkOTP(ctx context.Context, phoneNumber, code string, purpose auth.OtpPurpose) (auth.Otp, error) {
	// find otp attempt
	otp, err := a.repo.GetValidOtp(ctx, phoneNumber, purpose)
	if err != nil {
		return auth.Otp{}, err
	}

	if otp.Attempts > maxAttempts {
		return auth.Otp{}, auth.ErrTooManyAttempts
	}

	// validate the code send
	if ok := a.hasher.Verify(otp.CodeHash, phoneNumber, code); !ok {
		if _, err := a.repo.BumpOtpAttempts(ctx, otp.ID.String()); err != nil {
			a.logger.Error(ctx, "Auth.checkOTP: failed to bump otp attempt", "err", err)
			return auth.Otp{}, fmt.Errorf("failed to bump otp attempt: %w", err)
		}
		return auth.Otp{}, auth.ErrInvalidCode
	}

	return otp, nil
}

// statusCanAuth rejects moderated accounts. pending stays allowed — it is
// reserved for non-self-service creation and must still be a working login.
func statusCanAuth(s auth.AccountStatus) error {
	switch s {
	case auth.AccountStatusSuspended:
		return auth.ErrAccountSuspended
	case auth.AccountStatusBanned:
		return auth.ErrAccountBanned
	}
	return nil
}

// getCodeAndHash returns the 6-digit code to send for phoneE164 and its
// hash. The seeded test accounts keep the fixed auth.TestOtpCode so the team
// can drive register/login/reset without reading an SMS; every other number
// gets a fresh code from crypto/rand.
func (a Auth) getCodeAndHash(phoneE164 string) (string, string, error) {
	if auth.IsTestPhone(phoneE164) {
		return auth.TestOtpCode, a.hasher.Hash(phoneE164, auth.TestOtpCode), nil
	}

	n, err := rand.Int(rand.Reader, big.NewInt(codeSpace))
	if err != nil {
		return "", "", fmt.Errorf("drawing otp code: %w", err)
	}
	code := fmt.Sprintf("%06d", n.Int64())

	return code, a.hasher.Hash(phoneE164, code), nil
}
