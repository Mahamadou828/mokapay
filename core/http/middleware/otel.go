package middleware

import (
	"context"
	"net/http"

	"go.opentelemetry.io/otel/trace"
	core "mokapay.app/core/http"
	"mokapay.app/core/otel"
)

// Otel starts the otel tracing and stores the trace id in the context.
func Otel(tracer trace.Tracer) core.MidFunc {
	m := func(next core.HandlerFunc) core.HandlerFunc {
		h := func(ctx context.Context, r *http.Request) core.Encoder {
			ctx = otel.InjectTracing(ctx, tracer)

			return next(ctx, r)
		}

		return h
	}

	return m
}
