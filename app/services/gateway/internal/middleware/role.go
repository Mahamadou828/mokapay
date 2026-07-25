package middleware

import (
	"context"
	"errors"
	"net/http"

	"mokapay.app/app/services/gateway/internal/sdk/errs"
	coreerrs "mokapay.app/core/error"
	core "mokapay.app/core/http"
	"mokapay.app/core/passport"
)

// RequireRoles gates a route on the caller's role (DB enum strings: "ops",
// "admin", ...). Runs after Auth — the passport must already be in context.
func RequireRoles(roles ...string) core.MidFunc {
	return func(next core.HandlerFunc) core.HandlerFunc {
		return func(ctx context.Context, r *http.Request) core.Encoder {
			p, err := passport.FromContext(ctx)
			if err != nil {
				return coreerrs.New(errs.Unauthenticated, errors.New("authentication required"))
			}

			for _, role := range roles {
				if p.User.Role == role {
					return next(ctx, r)
				}
			}

			return coreerrs.New(errs.PermissionDenied, errors.New("insufficient role"))
		}
	}
}
