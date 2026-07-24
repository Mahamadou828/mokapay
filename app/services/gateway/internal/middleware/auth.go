package middleware

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
	"mokapay.app/app/services/gateway/internal/sdk/errs"
	core "mokapay.app/core/http"
	"mokapay.app/core/passport"
)

type AuthValidator interface {
	ValidateToken(token string) (jwt.RegisteredClaims, error)
}

// Auth validates signature, expiry, issuer, client and token type.
// Currently base on aws cognito integration.
func Auth(v AuthValidator) core.MidFunc {
	return func(next core.HandlerFunc) core.HandlerFunc {
		return func(ctx context.Context, r *http.Request) core.Encoder {
			token := r.Header.Get("Authorization")
			if token == "" {
				return core.NewHTTPError(errs.PermissionDenied, errors.New("authorization require"))
			}

			token, ok := strings.CutPrefix(token, "Bearer")
			if !ok {
				return core.NewHTTPError(errs.PermissionDenied, errors.New("invalid token"))
			}

			cl, err := v.ValidateToken(token)
			if err != nil {
				return core.NewHTTPError(errs.PermissionDenied, err)
			}

			ctx = passport.AddToContext(ctx, passport.Passport{
				Claim: cl,
			})

			return next(ctx, r)
		}
	}
}
