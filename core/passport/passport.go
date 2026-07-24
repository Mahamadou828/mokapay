// Package passport - provide a implementation of auth in context
// TODO: we need to decide how we want, if we want to include the user information in the passport.
// Should we always rehydrate/fetch user information?
// For let's put the bare minimum
package passport

import (
	"context"
	"errors"

	"github.com/golang-jwt/jwt/v5"
)

type Type string

const (
	TypeUser   Type = "user"
	TypeSystem Type = "system"
)

const passID = ""

type ctxKey int

const (
	passportKey ctxKey = iota + 1
)

type User struct {
	ID                string  `json:"id"`
	FirstName         string  `json:"first_name"`
	LastName          string  `json:"last_name"`
	Email             *string `json:"email"`
	PhoneNumber       string  `json:"phone_number"`
	Role              string  `json:"role"`
	PreferredLanguage string  `json:"preferred_language"`
}

type Passport struct {
	Claim jwt.RegisteredClaims
	User  User
	Type  Type
}

func AddToContext(ctx context.Context, p *Passport) context.Context {
	return context.WithValue(ctx, passportKey, p)
}

func FromContext(ctx context.Context) (*Passport, error) {
	// AddToContext stores the pointer — assert the same type.
	v, ok := ctx.Value(passportKey).(*Passport)
	if !ok || v == nil {
		return nil, errors.New("passport not found in context")
	}
	return v, nil
}

func SystemPassport() *Passport {
	return &Passport{
		Claim: jwt.RegisteredClaims{
			Subject: "system",
		},
		User: User{
			FirstName: "System",
			LastName:  "System",
			ID:        passID,
			Role:      "system",
		},
		Type: TypeSystem,
	}
}
