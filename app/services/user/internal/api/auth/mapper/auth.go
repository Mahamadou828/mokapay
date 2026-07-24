// Package mapper converts between the auth business types and their proto
// counterparts.
package mapper

import (
	"time"

	"mokapay.app/app/services/user/internal/business/auth"
	userv1 "mokapay.app/proto/codebase/user/v1"
)

// RegisterToDomain maps the register payload; the code and password stay
// auth-flow fields that the repo never persists.
func RegisterToDomain(in *userv1.RegisterRequest) auth.NewAccount {
	return auth.NewAccount{
		PhoneNumber:       in.PhoneNumber,
		Code:              in.Code,
		Password:          in.Password,
		FirstName:         in.FirstName,
		LastName:          in.LastName,
		PreferredLanguage: LanguageFromProto(in.PreferredLanguage),
		Email:             in.Email,
	}
}

// TokenToAuthResponse maps an issued token pair; access_expires_in is the
// remaining lifetime in seconds at mapping time.
func TokenToAuthResponse(t auth.Token) *userv1.AuthResponse {
	return &userv1.AuthResponse{
		AccessToken:     t.AccessToken,
		RefreshToken:    t.RefreshToken,
		AccessExpiresIn: SecondsUntil(t.ExpiresAt),
	}
}

// SecondsUntil converts an absolute expiry to the relative form the protos
// carry (whole seconds, never negative).
func SecondsUntil(t time.Time) int64 {
	s := int64(time.Until(t).Seconds())
	if s < 0 {
		return 0
	}
	return s
}
