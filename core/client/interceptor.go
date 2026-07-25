package client

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"mokapay.app/core/passport"
	passportv1 "mokapay.app/proto/codebase/passport/v1"
)

const (
	PassportMetadataKey = "passport-metadata"
)

// UnaryClientInterceptor forwards the caller's passport as gRPC metadata.
// A context without a passport passes through untouched — public flows
// (login, register) are anonymous by design; the receiving handlers decide
// whether an identity is required.
func UnaryClientInterceptor() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		p, err := passport.FromContext(ctx)
		if err != nil {
			return invoker(ctx, method, req, reply, cc, opts...)
		}

		pStr, err := encodePassport(p)
		if err != nil {
			return fmt.Errorf("encode passport failed: %v", err)
		}

		md, ok := metadata.FromOutgoingContext(ctx)
		if !ok {
			md = metadata.New(nil)
		} else {
			md = md.Copy()
		}

		md.Append(PassportMetadataKey, pStr)
		ctx = metadata.NewOutgoingContext(ctx, md)
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

// UnaryServerInterceptor rebuilds the passport from incoming metadata. A
// request without passport metadata is served without one — anonymous
// (public) and service-internal calls are legitimate; handlers that need an
// identity fail on passport.FromContext. A present-but-corrupt passport is
// rejected: that is never a healthy caller.
func UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return handler(ctx, req)
		}

		passportMD, ok := md[PassportMetadataKey]
		if !ok || len(passportMD) == 0 || passportMD[0] == "" {
			return handler(ctx, req)
		}

		p, err := decodePassport(passportMD[0])
		if err != nil {
			return nil, status.Error(codes.Unauthenticated, "malformed passport metadata")
		}

		ctx = passport.AddToContext(ctx, p)
		return handler(ctx, req)
	}
}

func encodePassport(p *passport.Passport) (string, error) {
	passType := passportv1.PassportType_PASSPORT_TYPE_UNSPECIFIED
	switch p.Type {
	case passport.TypeUser:
		passType = passportv1.PassportType_PASSPORT_TYPE_USER
	case passport.TypeSystem:
		passType = passportv1.PassportType_PASSPORT_TYPE_SYSTEM
	}

	passProto := &passportv1.Passport{
		Claim: &passportv1.Claim{
			Issuer:       p.Claim.Issuer,
			Subject:      p.Claim.Subject,
			ClaimStrings: p.Claim.Audience,
			ExpiresAt:    numericDateToProto(p.Claim.ExpiresAt),
			NotBefore:    numericDateToProto(p.Claim.NotBefore),
			IssuedAt:     numericDateToProto(p.Claim.IssuedAt),
			Id:           p.Claim.ID,
		},
		User: &passportv1.User{
			Id:                p.User.ID,
			PhoneNumber:       p.User.PhoneNumber,
			FirstName:         p.User.FirstName,
			LastName:          p.User.LastName,
			PreferredLanguage: p.User.PreferredLanguage,
			Role:              p.User.Role,
			Email:             p.User.Email,
		},
		Type: passType,
	}

	protoEncoded, err := proto.Marshal(passProto)
	if err != nil {
		return "", fmt.Errorf("failed to encode passport: %w", err)
	}

	return base64.StdEncoding.EncodeToString(protoEncoded), nil
}

func decodePassport(encodedData string) (*passport.Passport, error) {
	protoDecoded, err := base64.StdEncoding.DecodeString(encodedData)
	if err != nil {
		return nil, fmt.Errorf("failed to decode passport: %w", err)
	}
	var passProto passportv1.Passport
	err = proto.Unmarshal(protoDecoded, &passProto)

	if err != nil {
		return nil, fmt.Errorf("failed to decode passport: %w", err)
	}

	out := &passport.Passport{}

	switch passProto.Type {
	case passportv1.PassportType_PASSPORT_TYPE_USER:
		out.Type = passport.TypeUser
	case passportv1.PassportType_PASSPORT_TYPE_SYSTEM:
		out.Type = passport.TypeSystem
	}

	if c := passProto.Claim; c != nil {
		out.Claim = jwt.RegisteredClaims{
			Issuer:    c.Issuer,
			Subject:   c.Subject,
			Audience:  c.ClaimStrings,
			ExpiresAt: protoToNumericDate(c.ExpiresAt),
			NotBefore: protoToNumericDate(c.NotBefore),
			IssuedAt:  protoToNumericDate(c.IssuedAt),
			ID:        c.Id,
		}
	}

	if u := passProto.User; u != nil {
		out.User = passport.User{
			ID:                u.Id,
			FirstName:         u.FirstName,
			LastName:          u.LastName,
			Email:             u.Email,
			PhoneNumber:       u.PhoneNumber,
			Role:              u.Role,
			PreferredLanguage: u.PreferredLanguage,
		}
	}

	return out, nil
}

// numericDateToProto converts a jwt claim date; nil stays nil (jwt treats
// absent dates as "no constraint").
func numericDateToProto(d *jwt.NumericDate) *timestamppb.Timestamp {
	if d == nil {
		return nil
	}
	return timestamppb.New(d.Time)
}

func protoToNumericDate(t *timestamppb.Timestamp) *jwt.NumericDate {
	if t == nil {
		return nil
	}
	return jwt.NewNumericDate(t.AsTime())
}
