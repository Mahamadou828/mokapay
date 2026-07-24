// Package passport - provide a implementation of auth in context
// TODO: we need to decide how we want, if we want to include the user information in the passport.
// Should we always rehydrate/fetch user information?
// For let's put the bare minimum
package passport

import (
	"context"

	"github.com/golang-jwt/jwt/v5"
)

type ctxKey int

const (
	passportKey ctxKey = iota + 1
)

type Passport struct {
	Claim jwt.RegisteredClaims
}

func AddToContext(ctx context.Context, p Passport) context.Context {
	return context.WithValue(ctx, passportKey, p)
}

func GetFromContext(ctx context.Context) *Passport {
	v, ok := ctx.Value(passportKey).(Passport)
	if !ok {
		return nil
	}
	return &v
}
