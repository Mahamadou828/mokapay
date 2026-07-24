package usecase

import (
	"context"
	"errors"
	"fmt"
	"time"

	"mokapay.app/app/services/user/internal/business/auth"
	"mokapay.app/app/services/user/internal/business/auth/repo"
	coredb "mokapay.app/core/db"
	"mokapay.app/core/logger"
)

const codeSpace = 1_000_000 // 6 digits: 000000-999999

type OTPSender interface {
	Send(ctx context.Context, phoneNumber, msg string) error
}

type AuthProvider interface {
	SignUp(ctx context.Context, phone, pwd string) (sub string, err error)
	Login(ctx context.Context, phone, password string) (accessToken, refreshToken string, expiresIn time.Duration, err error)
	Refresh(ctx context.Context, refreshToken, sub string) (accessToken, newRefreshToken string, expiresIn time.Duration, err error)
	ResetPassword(ctx context.Context, phone, newPwd string) error
	Logout(ctx context.Context, sub string) error
}

type Auth struct {
	logger   *logger.Logger
	repo     repo.Store
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
		hasher:   h,
	}, nil
}

func (a Auth) SendOTP(ctx context.Context, phoneNumber string, purpose auth.OtpPurpose) error {
	now := time.Now()
	// check if phone number is correct
	phoneNumber, err := normalizePhoneNumber(phoneNumber)
	if err != nil {
		a.logger.Error(ctx, "Auth.SendOTP: failed to parse phone number", "err", err)
		return auth.ErrInvalidPhoneNumber
	}

	// check if the user already has such otp open
	otp, err := a.repo.GetValidOtp(ctx, phoneNumber, purpose)
	if err != nil {
		if !errors.Is(err, auth.ErrOtpNotFound) {
			a.logger.Error(ctx, "Auth.SendOTP: GetValidOtp error: %v", err)
			return err
		}
	}

	// Previous otp found
	if err == nil {
		// too many attempt
		if otp.Attempts > 5 {
			return auth.ErrTooManyAttempts
		}

		// resend a new code
		code, hashCode, err := a.getCodeAndHash(phoneNumber)
		if err != nil {
			a.logger.Error(ctx, "Auth.SendOTP: getCode: %v", err)
			return err
		}
		// send otp
		if err := a.otp.Send(ctx, phoneNumber, code); err != nil {
			a.logger.Error(ctx, "Auth.SendOTP: failed to send otp request", "err", err)
			return err
		}

		// update code hash in otp and attempt
		if _, err := a.repo.RefreshOtp(ctx, otp.ID.String(), hashCode, now.Add(time.Minute*5)); err != nil {
			a.logger.Error(ctx, "Auth.SendOTP: failed to refresh otp", "err", err)
			return err
		}

		return nil
	}

	code, hashCode, err := a.getCodeAndHash(phoneNumber)
	if err != nil {
		a.logger.Error(ctx, "Auth.SendOTP: failed to get code", "err", err)
		return fmt.Errorf("otp: generate: %w", err)
	}

	// send otp
	if err := a.otp.Send(ctx, phoneNumber, code); err != nil {
		a.logger.Error(ctx, "Auth.SendOTP: failed to send otp request", "err", err)
		return err
	}

	// create otp attempt
	if _, err := a.repo.AddOtp(ctx, auth.NewOtp{
		PhoneNumber: phoneNumber,
		CodeHash:    hashCode,
		Purpose:     purpose,
		ExpiresAt:   now.Add(time.Minute * 5),
	}); err != nil {
		a.logger.Error(ctx, "Auth.SendOTP: failed to create otp attempt", "err", err)
		return err
	}

	return nil
}

func (a Auth) RegisterUser(ctx context.Context, na auth.NewAccount) (auth.Token, error) {
	now := time.Now()

	// check otp for phone number validation
	phoneNumber, err := normalizePhoneNumber(na.PhoneNumber)
	if err != nil {
		a.logger.Error(ctx, "Auth.RegisterUser: failed to parse phone number", "err", err)
		return auth.Token{}, auth.ErrInvalidPhoneNumber
	}

	// find otp attempt
	otp, err := a.repo.GetValidOtp(ctx, phoneNumber, auth.OtpPurposeRegister)
	if err != nil {
		return auth.Token{}, err
	}
	// if the only attempt available has reached 5 attempt we need to send an error
	if otp.Attempts > 5 {
		return auth.Token{}, auth.ErrTooManyAttempts
	}

	// validate the code send
	if ok := a.hasher.Verify(otp.CodeHash, phoneNumber, na.Code); !ok {
		if _, err := a.repo.BumpOtpAttempts(ctx, otp.ID.String()); err != nil {
			a.logger.Error(ctx, "Auth.RegisterUser: failed to bump otp attempt", "err", err)
			return auth.Token{}, fmt.Errorf("failed to bump otp attempt: %w", err)
		}
		return auth.Token{}, auth.ErrInvalidCode
	}

	if _, err := a.repo.ConsumeOtp(ctx, otp.ID.String()); err != nil {
		a.logger.Error(ctx, "Auth.RegisterUser: failed to consume otp attempt", "err", err)
		return auth.Token{}, fmt.Errorf("failed to consume otp attempt: %w", err)
	}

	// create user account in auth provider
	sub, err := a.provider.SignUp(ctx, phoneNumber, na.Password)
	if err != nil {
		a.logger.Error(ctx, "")
		return auth.Token{}, fmt.Errorf("failed to sign up user: %v", err)
	}

	// create user account
	if _, err := a.repo.AddAccount(ctx, na, sub); err != nil {
		a.logger.Error(ctx, "Auth.RegisterUser: failed to add account", "err", err)
		return auth.Token{}, fmt.Errorf("failed to add account: %w", err)
	}

	// get tokens
	accessToken, refreshToken, expiresIn, err := a.provider.Login(ctx, phoneNumber, na.Password)
	if err != nil {
		a.logger.Error(ctx, "Auth.RegisterUser: failed to login user", "err", err)
		return auth.Token{}, fmt.Errorf("failed to login user: %v", err)
	}

	return auth.Token{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresAt:    now.Add(expiresIn),
	}, nil
}

func (a Auth) Login(ctx context.Context, phone, password string) (auth.Token, error) {
	now := time.Now()

	// login user from provider
	accessToken, refreshToken, expiresIn, err := a.provider.Login(ctx, phone, password)
	if err != nil {
		a.logger.Error(ctx, "Auth.Login: failed to login user", "err", err)
		return auth.Token{}, fmt.Errorf("failed to login user: %v", err)
	}

	return auth.Token{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresAt:    now.Add(expiresIn),
	}, err

}
func (a Auth) RefreshToken(ctx context.Context, refreshToken, sub string) (auth.Token, error) {
	now := time.Now()

	// refresh token from provider
	accessToken, refreshToken, expiresIn, err := a.provider.Refresh(ctx, refreshToken, sub)
	if err != nil {
		a.logger.Error(ctx, "Auth.RefreshToken: failed to refresh token", "err", err)
		return auth.Token{}, fmt.Errorf("failed to refresh token: %v", err)
	}

	return auth.Token{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresAt:    now.Add(time.Duration(expiresIn)),
	}, err
}

func (a Auth) ChangePassword(ctx context.Context, phoneNumber, code, newPwd string) error {
	// find otp attempt
	otp, err := a.repo.GetValidOtp(ctx, phoneNumber, auth.OtpPurposeReset)
	if err != nil {
		return err
	}

	if otp.Attempts > 5 {
		return auth.ErrTooManyAttempts
	}

	// validate the code send
	if ok := a.hasher.Verify(otp.CodeHash, phoneNumber, code); !ok {
		if _, err := a.repo.BumpOtpAttempts(ctx, otp.ID.String()); err != nil {
			a.logger.Error(ctx, "Auth.RegisterUser: failed to bump otp attempt", "err", err)
			return fmt.Errorf("failed to bump otp attempt: %w", err)
		}
		return auth.ErrInvalidCode
	}

	// reset password in provider
	if err := a.provider.ResetPassword(ctx, phoneNumber, newPwd); err != nil {
		a.logger.Error(ctx, "Auth.RegisterUser: failed to reset password", "err", err)
		return fmt.Errorf("failed to reset password: %w", err)
	}

	return nil
}

func (a Auth) LogOut(ctx context.Context, sub string) error {
	return a.provider.Logout(ctx, sub)
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
