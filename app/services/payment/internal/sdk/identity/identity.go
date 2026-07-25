// Package identity resolves and polices the gateway-forwarded passport
// for the payment handlers. Internal RPCs never sit behind the gateway's
// RequireRoles — the passport check here IS the authorization.
package identity

import (
	"context"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"mokapay.app/core/passport"
)

// RequireSystemOrRoles admits a system passport or a user passport whose
// role is in the set. Returns the acting account id (uuid.Nil for
// system callers).
func RequireSystemOrRoles(ctx context.Context, roles ...string) (uuid.UUID, error) {
	pass, err := passport.FromContext(ctx)
	if err != nil {
		return uuid.UUID{}, status.Error(codes.Unauthenticated, "no passport in context")
	}
	if pass.IsSystem() {
		return uuid.Nil, nil
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

// RequireSystem admits only the system passport (service-internal RPCs —
// the file worker's proof reporting).
func RequireSystem(ctx context.Context) error {
	pass, err := passport.FromContext(ctx)
	if err != nil || !pass.IsSystem() {
		return status.Error(codes.PermissionDenied, "service-internal rpc")
	}
	return nil
}

// RequireRoles admits only a user passport with one of the roles (no
// system bypass — decisions need an accountable human).
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
