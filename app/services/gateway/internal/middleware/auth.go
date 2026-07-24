package middleware

import (
	"context"
	"errors"
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
func Auth(v AuthValidator, client client.MokaClient) core.MidFunc {
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

			cl, err := v.ValidateToken(strings.TrimSpace(token))
			if err != nil {
				return coreerrs.New(errs.PermissionDenied, err)
			}

			// rehydrate user data
			// TODO: put caching here to not fetch on every request
			var user *userv1.Account
			{
				pass := passport.SystemPassport()
				ctx := passport.AddToContext(r.Context(), pass)

				user, err = client.User.User.GetAccountBySub(ctx, &userv1.GetAccountBySubRequest{
					Sub: cl.Subject,
				})
				if err != nil {
					return coreerrs.New(errs.PermissionDenied, err)
				}
			}
			if user == nil {
				return coreerrs.New(errs.PermissionDenied, errors.New("invalid token"))
			}

			ctx = passport.AddToContext(ctx, &passport.Passport{
				Claim: cl,
				User: passport.User{
					ID:                user.Id,
					FirstName:         user.FirstName,
					LastName:          user.LastName,
					Email:             user.Email,
					PhoneNumber:       user.PhoneNumber,
					Role:              user.Role.String(),
					PreferredLanguage: user.PreferredLanguage.String(),
				},
				Type: passport.TypeUser,
			})

			return next(ctx, r)
		}
	}
}
