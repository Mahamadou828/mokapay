package grpc

import "context"

type Logger func(ctx context.Context, msg string, args ...any)
