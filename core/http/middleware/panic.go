package middleware

import (
	"context"
	"net/http"
	"runtime/debug"

	core "mokapay.app/core/http"
)

// Panics recovers from panics and converts the panic to an error so it is
// reported in Metrics and handled in Errors.
func Panics(log core.Logger) core.MidFunc {
	m := func(next core.HandlerFunc) core.HandlerFunc {
		h := func(ctx context.Context, r *http.Request) core.Encoder {

			// Defer a function to recover from a panic and set the err return
			// variable after the fact.
			defer func() {
				if rec := recover(); rec != nil {
					trace := debug.Stack()
					err := core.Errorf(errInternalOnlyLog, "PANIC [%v] TRACE[%s]", rec, string(trace))
					log(ctx, "", err)
				}
			}()

			return next(ctx, r)
		}

		return h
	}

	return m
}
