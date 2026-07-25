package auth

import (
	"context"
	"errors"
	"fmt"
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

type OTPSender interface {
	Send(ctx context.Context, phoneNumber, msg string) error
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
	provider AuthProvider
	hasher   Hasher
}

func New(l *logger.Logger, db *coredb.Postgres, otp OTPSender, p AuthProvider, hash string) (Auth, error) {
	h, err := NewHasher([]byte(hash))
	if err != nil {
		return Auth{}, err
	}

	return Auth{
		repo:     repo.New(db),
		otp:      otp,
		provider: p,
		logger:   l,
		db:       db,
		hasher:   h,
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
		if err := a.otp.Send(ctx, phoneNumber, code); err != nil {
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
	if err := a.otp.Send(ctx, phoneNumber, code); err != nil {
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

// CheckAndConsumeOTP verifies the code sent for phoneNumber/purpose and burns
// it on success. phoneNumber must already be in E.164 form — codes are stored
// under it. A failed verify counts against the attempts cap.
func (a Auth) CheckAndConsumeOTP(ctx context.Context, phoneNumber, code string, purpose auth.OtpPurpose) error {
	// find otp attempt
	otp, err := a.repo.GetValidOtp(ctx, phoneNumber, purpose)
	if err != nil {
		return err
	}

	if otp.Attempts > maxAttempts {
		return auth.ErrTooManyAttempts
	}

	// validate the code send
	if ok := a.hasher.Verify(otp.CodeHash, phoneNumber, code); !ok {
		if _, err := a.repo.BumpOtpAttempts(ctx, otp.ID.String()); err != nil {
			a.logger.Error(ctx, "Auth.CheckAndConsumeOTP: failed to bump otp attempt", "err", err)
			return fmt.Errorf("failed to bump otp attempt: %w", err)
		}
		return auth.ErrInvalidCode
	}

	if _, err := a.repo.ConsumeOtp(ctx, otp.ID.String()); err != nil {
		a.logger.Error(ctx, "Auth.CheckAndConsumeOTP: failed to consume otp attempt", "err", err)
		return fmt.Errorf("failed to consume otp attempt: %w", err)
	}

	return nil
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

// getCode - return a random 6 digits code and the hash
// TODO: remove harcoded value of 111111
func (a Auth) getCodeAndHash(p string) (string, string, error) {
	//n, err := rand.Int(rand.Reader, big.NewInt(codeSpace))
	//if err != nil {
	//	return err
	//}
	//code := fmt.Sprintf("%06d", n.Int64())
	code := "111111"
	return code, a.hasher.Hash(p, code), nil
}
