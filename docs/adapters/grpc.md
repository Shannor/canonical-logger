# gRPC Unary Server Interceptor Recipe

The same 4-step **Wide Event per Unit of Work** pattern applies to gRPC servers. Pair `UnaryServerInterceptor` with `clog.RunValue` inside your RPC methods so outcomes and return values flow through cleanly.

---

## Recipe: `UnaryServerInterceptor`

```go
package grpclog

import (
	"context"
	"log/slog"
	"time"

	"github.com/Shannor/canonical-logger"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

func UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		start := time.Now()
		ctx = clog.NewContext(ctx)
		bag := clog.BagFromContext(ctx)

		resp, err := handler(ctx, req)

		stop := time.Since(start)
		code := status.Code(err)

		level := slog.LevelInfo
		if err != nil || bag.HasGroup("error") {
			level = slog.LevelError
		} else if bag.HasWarning() {
			level = slog.LevelWarn
		}

		attrs := []slog.Attr{
			slog.String("grpc.method", info.FullMethod),
			slog.String("grpc.code", code.String()),
			slog.String("latency", stop.String()),
		}
		attrs = append(attrs, bag.Attrs()...)

		clog.LoggerFromContext(ctx).LogAttrs(ctx, level, bag.Message(), attrs...)
		return resp, err
	}
}
```

---

## Using `clog.RunValue` in gRPC Handlers

```go
func (s *UserServer) GetUser(ctx context.Context, req *pb.GetUserRequest) (*pb.User, error) {
	return clog.RunValue(ctx, "get-user", func(ctx context.Context) (*pb.User, error) {
		clog.WithGroupField(ctx, "request", "userId", req.GetId())
		return s.userService.Get(ctx, req.GetId())
	})
}
```
