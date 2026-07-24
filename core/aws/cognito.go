// Package aws - provide an implementation of aws cognito
// NOTE/TODO: WE NEED TO MOVE OTP FROM OUTSIDE OF COGNITO BACK INTO IT USING LAMBDA AS SOON AS WE FIGURE OUT THE PROVIDER PROBLEM
package aws

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	cip "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider/types"
	"github.com/golang-jwt/jwt/v5"
)

// Error list
var (
	ErrPhoneTaken      = errors.New("phone number already registered")
	ErrBadCredentials  = errors.New("invalid phone or password")
	ErrNotConfirmed    = errors.New("account not confirmed")
	ErrCodeMismatch    = errors.New("invalid verification code")
	ErrCodeExpired     = errors.New("verification code expired")
	ErrPasswordPolicy  = errors.New("password does not meet policy")
	ErrTooManyRequests = errors.New("rate limited, retry later")
	ErrInvalidToken    = errors.New("invalid token")
	ErrFailToLogOut    = errors.New("fail to logout user")
)

// Token - aws cognito jwt token
type Token struct {
	AccessToken  string
	IDToken      string
	RefreshToken string // empty on refresh unless rotation is enabled
	ExpiresAt    time.Time
}

type Claims struct {
	Sub      string
	Username string
	ClientID string
	TokenUse string
	jwt.RegisteredClaims
}

// Cognito - aws cognito client implementation
type Cognito struct {
	clientID     string
	clientSecret string
	poolID       string
	region       string
	client       *cip.Client
	k            keyfunc.Keyfunc
}

func NewCognito(ctx context.Context, clientID, clientSecret, poolID, region string) (*Cognito, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("https://cognito-idp.%s.amazonaws.com/%s/.well-known/jwks.json", region, poolID)
	k, err := keyfunc.NewDefaultCtx(ctx, []string{url})
	if err != nil {
		return nil, fmt.Errorf("load jwks: %w", err)
	}

	return &Cognito{
		clientID:     clientID,
		clientSecret: clientSecret,
		poolID:       poolID,
		region:       region,
		client:       cip.NewFromConfig(cfg),
		k:            k,
	}, nil
}

// SignUp create a new user account in aws cognito with phone number verified to true
func (c *Cognito) SignUp(ctx context.Context, phone, pwd string) (sub string, err error) {
	out, err := c.client.AdminCreateUser(ctx, &cip.AdminCreateUserInput{
		UserPoolId:    aws.String(c.poolID),
		Username:      aws.String(phone),
		MessageAction: types.MessageActionTypeSuppress, // don't send the invite SMS
		UserAttributes: []types.AttributeType{
			{Name: aws.String("phone_number"), Value: aws.String(phone)},
			{Name: aws.String("phone_number_verified"), Value: aws.String("true")},
		},
	})
	if err != nil {
		return "", mapErr(err)
	}

	// AdminCreateUser leaves the user in FORCE_CHANGE_PASSWORD. This clears it.
	if _, err = c.client.AdminSetUserPassword(ctx, &cip.AdminSetUserPasswordInput{
		UserPoolId: aws.String(c.poolID),
		Username:   aws.String(phone),
		Password:   aws.String(pwd),
		Permanent:  true,
	}); err != nil {
		return "", mapErr(err)
	}

	for _, a := range out.User.Attributes {
		if aws.ToString(a.Name) == "sub" {
			return aws.ToString(a.Value), nil
		}
	}
	return "", errors.New("sub not returned")
}

// Login uses ADMIN_USER_PASSWORD_AUTH to generate an auth token
func (c *Cognito) Login(ctx context.Context, phone, password string) (accessToken, refreshToken string, expiresIn time.Duration, err error) {
	out, err := c.client.AdminInitiateAuth(ctx, &cip.AdminInitiateAuthInput{
		UserPoolId: aws.String(c.poolID),
		ClientId:   aws.String(c.clientID),
		AuthFlow:   types.AuthFlowTypeAdminUserPasswordAuth,
		AuthParameters: map[string]string{
			"USERNAME":    phone,
			"PASSWORD":    password,
			"SECRET_HASH": c.secretHash(phone),
		},
	})
	if err != nil {
		return "", "", 0, mapErr(err)
	}

	if out.AuthenticationResult == nil {
		return "", "", 0, errors.New("failed to generate auth token")
	}

	return aws.ToString(out.AuthenticationResult.AccessToken),
		aws.ToString(out.AuthenticationResult.RefreshToken),
		time.Duration(out.AuthenticationResult.ExpiresIn) * time.Second,
		nil
}

// Refresh - note SECRET_HASH is computed from the user's sub, not the phone number.
func (c *Cognito) Refresh(ctx context.Context, refreshToken, sub string) (accessToken, newRefreshToken string, expiresIn time.Duration, err error) {
	out, err := c.client.AdminInitiateAuth(ctx, &cip.AdminInitiateAuthInput{
		UserPoolId: aws.String(c.poolID),
		ClientId:   aws.String(c.clientID),
		AuthFlow:   types.AuthFlowTypeRefreshTokenAuth,
		AuthParameters: map[string]string{
			"REFRESH_TOKEN": refreshToken,
			"SECRET_HASH":   c.secretHash(sub),
		},
	})
	if err != nil {
		return "", "", 0, mapErr(err)
	}

	if out.AuthenticationResult == nil {
		return "", "", 0, errors.New("failed to generate auth token")
	}

	return aws.ToString(out.AuthenticationResult.AccessToken),
		aws.ToString(out.AuthenticationResult.RefreshToken),
		time.Duration(out.AuthenticationResult.ExpiresIn) * time.Second,
		nil
}

// ResetPassword - force reset the user password bypassing phone verification.
// NOTE: We need to move otp inside cognito pool as soon as we find a provider.
func (c *Cognito) ResetPassword(ctx context.Context, phone, newPwd string) error {

	// Permanent: true is essential. Without it the user lands in
	// FORCE_CHANGE_PASSWORD and every subsequent login returns a
	// NEW_PASSWORD_REQUIRED challenge instead of tokens.
	if _, err := c.client.AdminSetUserPassword(ctx, &cip.AdminSetUserPasswordInput{
		UserPoolId: aws.String(c.poolID),
		Username:   aws.String(phone),
		Password:   aws.String(newPwd),
		Permanent:  true,
	}); err != nil {
		return mapErr(err)
	}

	// Critical: changing the password does NOT invalidate existing sessions.
	// If an attacker already had a refresh token, they keep it forever unless
	// we do this. Access tokens still survive until they expire — keep the
	// access token TTL short (15-60 min) for exactly this reason.
	if _, err := c.client.AdminUserGlobalSignOut(ctx, &cip.AdminUserGlobalSignOutInput{
		UserPoolId: aws.String(c.poolID),
		Username:   aws.String(phone),
	}); err != nil {
		// Log loudly. The password change already succeeded, so don't fail the
		// request, but this needs an alarm — it's a real security gap.
		return ErrFailToLogOut
	}
	return nil

}

// Logout revokes every refresh token for the user. Access tokens already issued
// stay valid until they expire — that is inherent to JWTs, plan your TTLs for it.
func (c *Cognito) Logout(ctx context.Context, sub string) error {
	if _, err := c.client.AdminUserGlobalSignOut(ctx, &cip.AdminUserGlobalSignOutInput{
		UserPoolId: aws.String(c.poolID),
		Username:   aws.String(sub),
	}); err != nil {
		return mapErr(err)
	}
	return nil
}

// AdminDelete removes the user from the pool. An already-deleted user is
// treated as success — callers use this to compensate a failed sign-up.
func (c *Cognito) AdminDelete(ctx context.Context, username string) error {
	if _, err := c.client.AdminDeleteUser(ctx, &cip.AdminDeleteUserInput{
		UserPoolId: aws.String(c.poolID),
		Username:   aws.String(username),
	}); err != nil {
		var notFound *types.UserNotFoundException
		if errors.As(err, &notFound) {
			return nil
		}
		return mapErr(err)
	}
	return nil
}

// ValidateToken - validate the given token in place.
func (c *Cognito) ValidateToken(token string) (jwt.RegisteredClaims, error) {
	issuer := fmt.Sprintf("https://cognito-idp.%s.amazonaws.com/%s", c.region, c.poolID)
	var cl Claims

	if _, err := jwt.ParseWithClaims(
		token,
		&cl,
		c.k.Keyfunc,
		jwt.WithIssuer(issuer),
		jwt.WithValidMethods([]string{"RS256"}),
		jwt.WithExpirationRequired(),
	); err != nil {
		return jwt.RegisteredClaims{}, err
	}

	if cl.TokenUse != "access" || cl.ClientID != c.clientID {
		return jwt.RegisteredClaims{}, ErrInvalidToken
	}

	return jwt.RegisteredClaims{
		Issuer:    cl.Issuer,
		Subject:   cl.Subject,
		Audience:  cl.Audience,
		ExpiresAt: cl.ExpiresAt,
		NotBefore: cl.NotBefore,
		IssuedAt:  cl.IssuedAt,
		ID:        cl.ID,
	}, nil
}

// secretHash = Base64(HMAC-SHA256(key: clientSecret, msg: username + clientID))
func (c *Cognito) secretHash(username string) string {
	m := hmac.New(sha256.New, []byte(c.clientSecret))
	m.Write([]byte(username + c.clientID))
	return base64.StdEncoding.EncodeToString(m.Sum(nil))
}

// mapErr map aws cognito internal error to ours
func mapErr(err error) error {
	if err == nil {
		return nil
	}
	var (
		exists     *types.UsernameExistsException
		notAuth    *types.NotAuthorizedException
		userNot    *types.UserNotFoundException
		notConf    *types.UserNotConfirmedException
		mismatch   *types.CodeMismatchException
		expired    *types.ExpiredCodeException
		invalidPwd *types.InvalidPasswordException
		tooMany    *types.TooManyRequestsException
		limitEx    *types.LimitExceededException
	)
	switch {
	case errors.As(err, &exists):
		return ErrPhoneTaken
	case errors.As(err, &notAuth), errors.As(err, &userNot):
		// Deliberately collapsed: don't leak whether the account exists.
		return ErrBadCredentials
	case errors.As(err, &notConf):
		return ErrNotConfirmed
	case errors.As(err, &mismatch):
		return ErrCodeMismatch
	case errors.As(err, &expired):
		return ErrCodeExpired
	case errors.As(err, &invalidPwd):
		return ErrPasswordPolicy
	case errors.As(err, &tooMany), errors.As(err, &limitEx):
		return ErrTooManyRequests
	}
	return err
}
