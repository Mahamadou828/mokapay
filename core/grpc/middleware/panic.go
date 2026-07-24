package middleware

import (
	"context"
	"runtime/debug"

	"google.golang.org/grpc"
	errs "mokapay.app/core/error"
	coregrpc "mokapay.app/core/grpc"
)

func PanicUnaryServerInterceptor(l coregrpc.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		defer func() {
			if rec := recover(); rec != nil {
				trace := debug.Stack()
				err := errs.Errorf(errInternalOnlyLog, "PANIC [%v] TRACE[%s]", rec, string(trace))
				l(ctx, "", err)
			}
		}()
		return handler(ctx, req)
	}
}
