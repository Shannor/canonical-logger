# Building Custom Adapters, Middlewares & Writers

`canonical-logger` (`clog`) intentionally ships as a **100% pure Go standard-library package** (`log/slog`, `context`, `net/http`) with zero external runtime dependencies.

Instead of bundling and maintaining dozens of third-party router, RPC, OpenAPI, and cloud SDK subpackages, `clog` exposes small, composable **Ports** in the root package. You can wire any framework or log sink into `clog` in ~20–40 lines of code inside your own application.

---

## 1. The Core Ports in `clog`

Every canonical logging integration uses four building blocks exported by `github.com/Shannor/canonical-logger`:

| Port / Function | Purpose |
| :--- | :--- |
| `clog.NewContext(ctx)` | Creates a fresh `*clog.FieldBag` (inheriting any context `*slog.Logger`) and returns the enriched `context.Context`. |
| `clog.BagFromContext(ctx)` | Retrieves the active `*clog.FieldBag` from `ctx` (or via registered `ContextResolver` hooks). |
| `clog.RegisterContextResolver(fn)` | Registers a fallback `func(ctx context.Context) *clog.FieldBag` for frameworks that wrap or replace `context.Context`. |
| `bag.HasGroup("error")` / `bag.HasWarning()` / `bag.Message()` / `bag.Attrs()` | Inspects accumulated state at the end of a request/task to determine log level and emit `[]slog.Attr`. |
| `clog.ExtractTrace(sentryTrace, baggage)` | Extracts `traceId` and `spanId` from incoming headers or generates a random 32-char hex `traceId`. |

---

## 2. How to Write a Custom HTTP / Router Middleware

Standard `net/http` routers (Go 1.22+ `http.ServeMux`, Chi, Gorilla Mux) work out of the box with `clog.HTTPMiddleware` and `clog.HTTPTraceMiddleware`.

For frameworks with custom context types (like **Gin**, **Echo**, or **Fiber**), every canonical middleware follows the same 4-step lifecycle:
1. **Initialize**: Create a `FieldBag` via `ctx := clog.NewContext(reqCtx)` and attach it to the request.
2. **Execute**: Call the next handler in the chain (`c.Next()` or `next(c)`).
3. **Evaluate Level**: Escalate to `slog.LevelError` if status >= 500, framework errors exist, or `bag.HasGroup("error")` is true; escalate to `slog.LevelWarn` if status >= 400 or `bag.HasWarning()` is true.
4. **Emit**: Call `clog.LoggerFromContext(ctx).LogAttrs(ctx, level, bag.Message(), attrs...)` combining root transport attributes with `bag.Attrs()...`.

### Recipe: Gin Middleware & TraceID

```go
package middleware

import (
	"context"
	"log/slog"
	"time"

	"github.com/Shannor/canonical-logger"
	"github.com/gin-gonic/gin"
)

const ginFieldBagKey = "__clog_fieldbag__"

func init() {
	// Register a fallback resolver so clog.BagFromContext(ctx) still finds the FieldBag
	// even if downstream middleware replaces c.Request with a context containing only gin.ContextKey.
	clog.RegisterContextResolver(func(ctx context.Context) *clog.FieldBag {
		if ginCtx, ok := ctx.Value(gin.ContextKey).(*gin.Context); ok && ginCtx != nil {
			if val, exists := ginCtx.Get(ginFieldBagKey); exists {
				if bag, ok := val.(*clog.FieldBag); ok {
					return bag
				}
			}
		}
		return nil
	})
}

func CanonicalLogger(sensitiveParams ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		ctx := clog.NewContext(c.Request.Context())
		bag := clog.BagFromContext(ctx)

		c.Set(ginFieldBagKey, bag)
		c.Request = c.Request.WithContext(ctx)

		c.Next()

		stop := time.Since(start)
		path := c.Request.URL.Path
		for _, param := range sensitiveParams {
			if c.Param(param) != "" {
				if fullPath := c.FullPath(); fullPath != "" {
					path = fullPath
					break
				}
			}
		}

		status := c.Writer.Status()
		level := slog.LevelInfo
		if status >= 500 || len(c.Errors) > 0 || bag.HasGroup("error") {
			level = slog.LevelError
		} else if status >= 400 || bag.HasWarning() {
			level = slog.LevelWarn
		}

		attrs := []slog.Attr{
			slog.Int("status", status),
			slog.String("method", c.Request.Method),
			slog.String("path", path),
			slog.String("ip", c.ClientIP()),
			slog.String("latency", stop.String()),
			slog.String("user-agent", c.Request.UserAgent()),
		}
		if len(c.Errors) > 0 {
			attrs = append(attrs, slog.String("gin_error", c.Errors.String()))
		}
		attrs = append(attrs, bag.Attrs()...)

		clog.LoggerFromContext(c.Request.Context()).LogAttrs(
			c.Request.Context(),
			level,
			bag.Message(),
			attrs...,
		)
	}
}

func TraceID() gin.HandlerFunc {
	return func(c *gin.Context) {
		traceID, spanID := clog.ExtractTrace(
			c.GetHeader(clog.HeaderSentryTrace),
			c.GetHeader(clog.HeaderBaggage),
		)

		ctx := clog.WithField(c.Request.Context(), "traceId", traceID)
		if spanID != "" {
			ctx = clog.WithField(ctx, "spanId", spanID)
		}
		c.Request = c.Request.WithContext(ctx)

		c.Header(clog.ResponseTraceHeader, traceID)
		c.Next()
	}
}
```

---

## 3. How to Write a Custom `ContextResolver` (e.g., `oapi-codegen`)

Some code generators or authentication middlewares (such as `github.com/oapi-codegen/gin-middleware`) construct a new `context.Context` and store the router context under their own context key.

Because `clog.RegisterContextResolver` accepts any `func(ctx context.Context) *clog.FieldBag`, you can register a resolver exclusively in services that use `oapi-codegen`—without coupling your non-OpenAPI services to `oapi-codegen`:

```go
package middleware

import (
	"context"

	"github.com/Shannor/canonical-logger"
	ginmiddleware "github.com/oapi-codegen/gin-middleware"
)

func init() {
	clog.RegisterContextResolver(func(ctx context.Context) *clog.FieldBag {
		if ginCtx := ginmiddleware.GetGinContext(ctx); ginCtx != nil {
			if val, exists := ginCtx.Get("__clog_fieldbag__"); exists {
				if bag, ok := val.(*clog.FieldBag); ok {
					return bag
				}
			}
		}
		return nil
	})
}
```

---

## 4. How to Write a gRPC Unary Server Interceptor

The exact same pattern applies to gRPC servers:

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

## 5. How to Write a Custom Log Sink / `io.Writer`

Because `clog` is built on standard `log/slog`, any `io.Writer` or `slog.Handler` works with `clog.NewJSONHandler(w, opts)`.

### Option A: Zero-Dependency Stdout JSON (Recommended for Cloud Run, GKE, Kubernetes, AWS ECS)

Container platforms automatically ingest structured JSON from `os.Stdout`. For Google Cloud Logging, `clog.GCPReplaceAttr` is built into the zero-dependency core:

```go
handler := clog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
	ReplaceAttr: clog.GCPReplaceAttr,
})
slog.SetDefault(slog.New(handler))
```

### Option B: Custom Google Cloud Logging SDK `io.Writer`

If your application streams logs directly via the `cloud.google.com/go/logging` SDK rather than `os.Stdout`, implement `io.Writer` in your application's logging setup:

```go
package gcpwriter

import (
	"encoding/json"
	"io"

	"cloud.google.com/go/logging"
)

type GCPWriter struct {
	Logger *logging.Logger
}

func (w *GCPWriter) Write(p []byte) (int, error) {
	if w.Logger == nil {
		return len(p), nil
	}

	fields := make(map[string]any)
	if err := json.Unmarshal(p, &fields); err != nil {
		w.Logger.Log(logging.Entry{Payload: string(p)})
		return len(p), nil
	}

	entry := logging.Entry{Payload: fields}
	if sev, ok := fields["severity"].(string); ok {
		entry.Severity = logging.ParseSeverity(sev)
	} else if lvl, ok := fields["level"].(string); ok {
		entry.Severity = logging.ParseSeverity(lvl)
	}

	w.Logger.Log(entry)
	return len(p), nil
}

var _ io.Writer = (*GCPWriter)(nil)
```
