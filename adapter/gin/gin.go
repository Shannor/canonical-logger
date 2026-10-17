// Package cloggin provides a Gin middleware adapter and OpenAPI context resolver for canonical-logger (clog).
package cloggin

import (
	"context"
	"log/slog"
	"time"

	"github.com/Shannor/canonical-logger"
	"github.com/gin-gonic/gin"
	ginmiddleware "github.com/oapi-codegen/gin-middleware"
)

const ginFieldBagKey = "__clog_fieldbag__"

func init() {
	clog.RegisterContextResolver(resolveGinContext)
}

func resolveGinContext(ctx context.Context) *clog.FieldBag {
	if ginCtx, ok := ctx.Value(gin.ContextKey).(*gin.Context); ok && ginCtx != nil {
		if val, exists := ginCtx.Get(ginFieldBagKey); exists {
			if bag, ok := val.(*clog.FieldBag); ok {
				return bag
			}
		}
	}

	if ginCtx := ginmiddleware.GetGinContext(ctx); ginCtx != nil {
		if val, exists := ginCtx.Get(ginFieldBagKey); exists {
			if bag, ok := val.(*clog.FieldBag); ok {
				return bag
			}
		}
	}

	return nil
}

// Option configures the Gin canonical logging middleware.
type Option func(*config)

// GinMiddlewareOption is an alias for Option for drop-in compatibility.
type GinMiddlewareOption = Option

type config struct {
	sensitiveParams []string
}

// WithSensitiveParams configures URL path parameters whose presence causes the
// router's full path template (e.g. "/users/:token") to be logged instead of the
// raw request path, preventing secrets from leaking into logs.
func WithSensitiveParams(params ...string) Option {
	return func(cfg *config) {
		cfg.sensitiveParams = append(cfg.sensitiveParams, params...)
	}
}

// Middleware returns a gin.HandlerFunc that logs requests using clog and log/slog.
// It creates a FieldBag per request, stores it in both the request context and gin.Context,
// and emits the canonical log line after the handler chain finishes.
func Middleware(opts ...Option) gin.HandlerFunc {
	cfg := &config{
		sensitiveParams: []string{"token", "optInID"},
	}
	for _, opt := range opts {
		opt(cfg)
	}

	return func(c *gin.Context) {
		start := time.Now()
		ctx := clog.NewContext(c.Request.Context())
		bag := clog.BagFromContext(ctx)
		c.Set(ginFieldBagKey, bag)
		c.Request = c.Request.WithContext(ctx)

		c.Next()

		stop := time.Since(start)

		path := c.Request.URL.Path
		for _, param := range cfg.sensitiveParams {
			if c.Param(param) != "" {
				if fullPath := c.FullPath(); fullPath != "" {
					path = fullPath
					break
				}
			}
		}

		logger := clog.LoggerFromContext(c.Request.Context())

		level := slog.LevelInfo
		var ginErrAttr slog.Attr
		hasGinErr := false
		if len(c.Errors) > 0 || bag.HasGroup("error") {
			level = slog.LevelError
			if len(c.Errors) > 0 {
				ginErrAttr = slog.String("gin_error", c.Errors.String())
				hasGinErr = true
			}
		} else if bag.HasWarning() {
			level = slog.LevelWarn
		}

		attrs := []slog.Attr{
			slog.Int("status", c.Writer.Status()),
			slog.String("method", c.Request.Method),
			slog.String("path", path),
			slog.String("ip", c.ClientIP()),
			slog.String("latency", stop.String()),
			slog.String("user-agent", c.Request.UserAgent()),
		}
		if hasGinErr {
			attrs = append(attrs, ginErrAttr)
		}
		attrs = append(attrs, bag.Attrs()...)

		logger.LogAttrs(c.Request.Context(), level, bag.Message(), attrs...)
	}
}

// GinMiddleware is an alias for Middleware.
func GinMiddleware(opts ...Option) gin.HandlerFunc {
	return Middleware(opts...)
}

// TraceID returns a gin.HandlerFunc that extracts a trace ID from incoming Sentry
// headers (sentry-trace or baggage), attaches traceId and spanId to the canonical log line,
// and sets the X-Trace-Id response header.
// Register this middleware AFTER Middleware().
func TraceID() gin.HandlerFunc {
	return func(c *gin.Context) {
		traceID, spanID := clog.ExtractTrace(
			c.GetHeader(clog.HeaderSentryTrace),
			c.GetHeader(clog.HeaderBaggage),
		)

		ctx := c.Request.Context()
		ctx = clog.WithField(ctx, "traceId", traceID)
		if spanID != "" {
			ctx = clog.WithField(ctx, "spanId", spanID)
		}
		c.Request = c.Request.WithContext(ctx)

		c.Header(clog.ResponseTraceHeader, traceID)
		c.Next()
	}
}
