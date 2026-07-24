// Package identity reads the caller's identity from incoming gRPC metadata.
// The gateway validates the JWT and forwards the Cognito sub under
// MetadataSubKey; handlers on authenticated RPCs resolve the caller with Sub.
package identity

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// MetadataSubKey is the metadata key the gateway sets from the validated
// token's sub claim.
const MetadataSubKey = "x-account-sub"

// Sub returns the caller's Cognito sub, or codes.Unauthenticated when the
// metadata is missing — an authenticated RPC reached us without the gateway
// having stamped an identity.
func Sub(ctx context.Context) (string, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", status.Error(codes.Unauthenticated, "missing caller identity")
	}

	vals := md.Get(MetadataSubKey)
	if len(vals) == 0 || vals[0] == "" {
		return "", status.Error(codes.Unauthenticated, "missing caller identity")
	}

	return vals[0], nil
}
