package shared

import (
	"context"
	"log/slog"
	"runtime/debug"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// LoggingInterceptor logs every unary RPC with method, duration, and status code.
func LoggingInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		elapsed := time.Since(start)

		code := codes.OK
		if err != nil {
			code = status.Code(err)
		}

		slog.Info("rpc",
			"method", info.FullMethod,
			"code", code.String(),
			"duration", elapsed.Round(time.Microsecond).String(),
		)
		return resp, err
	}
}

// RecoveryInterceptor recovers from panics in handlers, logs the stack trace,
// and returns an Internal error to the client instead of crashing.
func RecoveryInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (resp any, err error) {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("rpc: panic recovered",
					"method", info.FullMethod,
					"panic", r,
					"stack", string(debug.Stack()),
				)
				err = status.Errorf(codes.Internal, "internal error")
			}
		}()
		return handler(ctx, req)
	}
}
