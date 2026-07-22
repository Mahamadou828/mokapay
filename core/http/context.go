package http

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type ctxKey int

const (
	tracerKey ctxKey = iota + 1
)

func setTracer(ctx context.Context, tracer trace.Tracer) context.Context {
	return context.WithValue(ctx, tracerKey, tracer)
}

func addSpan(ctx context.Context, spanName string, keyValues ...attribute.KeyValue) (context.Context, trace.Span) {
	v, ok := ctx.Value(tracerKey).(trace.Tracer)
	if !ok || v == nil {
		return ctx, trace.SpanFromContext(ctx)
	}

	ctx, span := v.Start(ctx, spanName)
	span.SetAttributes(keyValues...)

	return ctx, span
}
