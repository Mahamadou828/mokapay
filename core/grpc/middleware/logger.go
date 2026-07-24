package middleware

import (
	"context"
	"fmt"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	coregrpc "mokapay.app/core/http"
)

func LoggerUnaryServerInterceptor(l coregrpc.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		now := time.Now()
		service, method := splitFullMethod(info.FullMethod)

		var remote string
		if p, ok := peer.FromContext(ctx); ok {
			remote = p.Addr.String()
		}

		l(ctx, "request started",
			"service", service,
			"method", method,
			"remoteaddr", remote,
		)

		resp, err := handler(ctx, req)

		args := []any{
			"service", service,
			"method", method,
			"remoteaddr", remote,
			"code", status.Code(err).String(),
			"since", time.Since(now).String(),
		}

		if err == nil {
			l(ctx, "request completed", args...)
			return resp, nil
		}

		args = append(args, "error", err.Error())

		if s, ok := status.FromError(err); ok {
			if d := s.Details(); len(d) > 0 {
				args = append(args, "details", fmt.Sprintf("%v", d))
			}
		} else {
			// Not a status error and no GRPCStatus() anywhere in the chain:
			// the client will see codes.Unknown. Flag it so it's greppable.
			args = append(args, "unmapped", true)
		}

		l(ctx, "request failed", args...)
		return resp, err
	}
}

func splitFullMethod(full string) (service, method string) {
	if i := strings.LastIndex(full, "/"); i >= 0 {
		return strings.TrimPrefix(full[:i], "/"), full[i+1:]
	}
	return "unknown", full
}
