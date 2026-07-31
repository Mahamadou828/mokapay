package middleware

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
	"mokapay.app/app/services/gateway/internal/sdk/errs"
	"mokapay.app/core/client"
	coreerrs "mokapay.app/core/error"
	userv1 "mokapay.app/proto/codebase/user/v1"

	core "mokapay.app/core/http"
	"mokapay.app/core/passport"
)

type AuthValidator interface {
	ValidateToken(token string) (jwt.RegisteredClaims, error)
}

// Auth validates signature, expiry, issuer, client and token type.
// Currently base on aws cognito integration.
func Auth(v AuthValidator, client *client.MokaClient) core.MidFunc {
	return func(next core.HandlerFunc) core.HandlerFunc {
		return func(ctx context.Context, r *http.Request) core.Encoder {
			token := r.Header.Get("Authorization")
			if token == "" {
				return coreerrs.New(errs.PermissionDenied, errors.New("authorization require"))
			}

			token, ok := strings.CutPrefix(token, "Bearer ")
			if !ok {
				return coreerrs.New(errs.PermissionDenied, errors.New("invalid token"))
			}

			pass, err := Authenticate(r.Context(), v, client, strings.TrimSpace(token))
			if err != nil {
				return coreerrs.New(errs.PermissionDenied, err)
			}

			return next(passport.AddToContext(ctx, pass), r)
		}
	}
}

// Authenticate is the single authentication path: validate the access token,
// then rehydrate the account behind its subject into a passport. The REST
// middleware above and the websocket handshake (which cannot carry an
// Authorization header, so its token rides the query string) both go through
// it — one auth path, two transports, no weaker second check.
func Authenticate(ctx context.Context, v AuthValidator, client *client.MokaClient, token string) (*passport.Passport, error) {
	cl, err := v.ValidateToken(token)
	if err != nil {
		return nil, err
	}

	// rehydrate user data
	// TODO: put caching here to not fetch on every request
	user, err := client.User.User.GetAccountBySub(
		passport.AddToContext(ctx, passport.SystemPassport()),
		&userv1.GetAccountBySubRequest{Sub: cl.Subject},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch user: %w", err)
	}
	if user == nil {
		return nil, errors.New("user not found")
	}

	return &passport.Passport{
		Claim: cl,
		User: passport.User{
			ID:                user.Id,
			FirstName:         user.FirstName,
			LastName:          user.LastName,
			Email:             user.Email,
			PhoneNumber:       user.PhoneNumber,
			Role:              roleString(user.Role),
			PreferredLanguage: languageString(user.PreferredLanguage),
		},
		Type: passport.TypeUser,
	}, nil
}

// roleString / languageString carry the DB enum strings in the passport —
// every service compares against those ("ops"), never the proto names
// ("ACCOUNT_ROLE_OPS").
func roleString(r userv1.AccountRole) string {
	switch r {
	case userv1.AccountRole_ACCOUNT_ROLE_USER:
		return "user"
	case userv1.AccountRole_ACCOUNT_ROLE_PENDING_AGENT:
		return "pending_agent"
	case userv1.AccountRole_ACCOUNT_ROLE_AGENT:
		return "agent"
	case userv1.AccountRole_ACCOUNT_ROLE_OPS:
		return "ops"
	case userv1.AccountRole_ACCOUNT_ROLE_ADMIN:
		return "admin"
	}
	return ""
}

func languageString(l userv1.Language) string {
	switch l {
	case userv1.Language_LANGUAGE_FR:
		return "fr"
	case userv1.Language_LANGUAGE_EN:
		return "en"
	}
	return ""
}
