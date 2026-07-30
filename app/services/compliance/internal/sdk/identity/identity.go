// Package identity resolves and polices the gateway-forwarded passport
// for the compliance handlers. The evaluation RPC never sits behind a
// gateway tier — the passport check here IS the authorization.
package identity

import (
	"context"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"mokapay.app/core/passport"
)

// RequireSystem admits only the system passport — the service-internal
// evaluation RPC, called by payment's River job and by nothing else.
func RequireSystem(ctx context.Context) error {
	pass, err := passport.FromContext(ctx)
	if err != nil || !pass.IsSystem() {
		return status.Error(codes.PermissionDenied, "service-internal rpc")
	}
	return nil
}

// RequireRoles admits only a user passport with one of the roles (no
// system bypass — a compliance decision needs an accountable human).
// Returns the acting account id.
func RequireRoles(ctx context.Context, roles ...string) (uuid.UUID, error) {
	pass, err := passport.FromContext(ctx)
	if err != nil {
		return uuid.UUID{}, status.Error(codes.Unauthenticated, "no passport in context")
	}
	for _, r := range roles {
		if pass.User.Role == r {
			id, err := uuid.Parse(pass.User.ID)
			if err != nil {
				return uuid.UUID{}, status.Error(codes.Unauthenticated, "passport carries no account id")
			}
			return id, nil
		}
	}
	return uuid.UUID{}, status.Error(codes.PermissionDenied, "insufficient role")
}
