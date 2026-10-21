# Gin Middleware & `ContextResolver` Recipe

For [Gin](`github.com/gin-gonic/gin`), the middleware opens a `FieldBag` per request, stores it on both `c.Request.Context()` and `*gin.Context`, and registers a fallback `ContextResolver` so `clog.BagFromContext(ctx)` always finds the bag even if a downstream middleware replaces `c.Request` with a context containing only `gin.ContextKey`.

---

## 1. Gin `CanonicalLogger` Middleware

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
```

---

## 2. Optional: `oapi-codegen/gin-middleware` `ContextResolver`

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
