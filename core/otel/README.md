# otel

A thin wrapper around [OpenTelemetry Go](https://opentelemetry.io/docs/languages/go/) that handles tracer setup, context propagation, and span management.

## Initializing tracing

Call `InitTracing` once at startup. It connects to a gRPC OTLP collector and registers the provider globally.

```go
import "template.app/core/otel"

tp, teardown, err := otel.InitTracing(otel.Config{
    ServiceName: "my-service",
    Host:        "localhost:4317",  // OTLP gRPC endpoint; leave empty to disable tracing
    Probability: 0.5,              // sample 50% of traces
    ExcludedRoutes: map[string]struct{}{
        "/healthz": {},
    },
})
if err != nil {
    log.Fatal(err)
}
defer teardown(ctx)
```

When `Host` is empty, a no-op provider is used — no traces are emitted and no connection is attempted.

## Injecting tracing into a request context

Call `InjectTracing` at the start of each request (e.g. in middleware) to attach a tracer and trace ID to the context:

```go
tracer := tp.Tracer("my-service")
ctx = otel.InjectTracing(ctx, tracer)
```

If an incoming trace ID is already present in the span context it is reused; otherwise a new UUID is generated.

## Reading the trace ID

```go
traceID := otel.GetTraceID(ctx)
```

Returns `"00000000000000000000000000000000"` when no trace ID is set. This value is compatible with `core/logger`'s `TraceIDFn`:

```go
logger.New(os.Stdout, logger.LevelInfo, "my-service", otel.GetTraceID)
```

## Adding spans

```go
import "go.opentelemetry.io/otel/attribute"

ctx, span := otel.AddSpan(ctx, "db.query",
    attribute.String("query", sql),
)
defer span.End()
```

If no tracer is found in the context the call is a no-op and the existing span is returned.

## Propagating traces across HTTP calls

Inject the current trace context into an outgoing HTTP request so downstream services continue the same trace:

```go
otel.AddTraceToRequest(ctx, req)
httpClient.Do(req)
```
