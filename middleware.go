package xlog

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

// GinMiddlewareOption allows configuring GinMiddleware behavior.
type GinMiddlewareOption func(*ginMiddlewareConfig)

type ginMiddlewareConfig struct {
	sensitiveParams []string
}

// WithSensitiveParams configures URL path parameters whose presence causes the
// router's full path template (e.g. "/users/:token") to be logged instead of the
// raw request path, preventing secrets from leaking into logs.
func WithSensitiveParams(params ...string) GinMiddlewareOption {
	return func(cfg *ginMiddlewareConfig) {
		cfg.sensitiveParams = append(cfg.sensitiveParams, params...)
	}
}

// GinMiddleware returns a gin.HandlerFunc that logs requests using xlog and Zerolog native context.
// It implements the canonical log line pattern by:
// 1. Creating a FieldBag per request and storing it in the request context.
// 2. After c.Next(), merging all accumulated fields (root + group dicts) into the canonical log event.
func GinMiddleware(opts ...GinMiddlewareOption) gin.HandlerFunc {
	cfg := &ginMiddlewareConfig{
		sensitiveParams: []string{"token", "optInID"},
	}
	for _, opt := range opts {
		opt(cfg)
	}

	return func(c *gin.Context) {
		start := time.Now()
		// Inject both the request-scoped logger and a fresh FieldBag into the context.
		// We also store the FieldBag on gin.Context so it survives any downstream
		// middleware (e.g. auth middleware) that replaces c.Request with a fresh context.
		ctx := log.Ctx(c.Request.Context()).With().Logger().WithContext(c.Request.Context())
		bag := &FieldBag{}
		ctx = context.WithValue(ctx, fieldBagCtxKey{}, bag)
		c.Set(ginFieldBagKey, bag)
		c.Request = c.Request.WithContext(ctx)

		// Process request
		c.Next()

		// Log canonical line only after request is processed
		stop := time.Since(start)

		path := c.Request.URL.Path
		// Capability routes with sensitive parameters must not
		// log the raw secret; log the route template for those requests.
		for _, param := range cfg.sensitiveParams {
			if c.Param(param) != "" {
				if fullPath := c.FullPath(); fullPath != "" {
					path = fullPath
					break
				}
			}
		}

		// Retrieve the current logger from context (which might have been enriched by handlers)
		// and finalize the canonical log event.
		finalLogger := log.Ctx(c.Request.Context())

		event := finalLogger.Info()
		if len(c.Errors) > 0 || bag.hasGroup("error") {
			event = finalLogger.Error()
			if len(c.Errors) > 0 {
				event = event.Str("gin_error", c.Errors.String())
			}
		} else if bag.hasWarning() {
			event = finalLogger.Warn()
		}

		event.
			Int("status", c.Writer.Status()).
			Str("method", c.Request.Method).
			Str("path", path).
			Str("ip", c.ClientIP()).
			Str("latency", stop.String()).
			Str("user-agent", c.Request.UserAgent())

		// Apply all accumulated FieldBag fields (root + group dicts) to the canonical event.
		bag.apply(event)
		event.Msg(bag.Message())
	}
}

// responseWriter wraps http.ResponseWriter to capture the HTTP status code.
type responseWriter struct {
	http.ResponseWriter
	statusCode  int
	wroteHeader bool
}

func (rw *responseWriter) WriteHeader(code int) {
	if !rw.wroteHeader {
		rw.statusCode = code
		rw.wroteHeader = true
	}
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	if !rw.wroteHeader {
		rw.WriteHeader(http.StatusOK)
	}
	return rw.ResponseWriter.Write(b)
}

// HTTPMiddleware returns a standard net/http middleware that implements
// the canonical log line pattern for any standard Go HTTP server or router.
func HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ctx := log.Ctx(r.Context()).With().Logger().WithContext(r.Context())
		bag := &FieldBag{}
		ctx = context.WithValue(ctx, fieldBagCtxKey{}, bag)
		r = r.WithContext(ctx)

		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(rw, r)

		stop := time.Since(start)
		finalLogger := log.Ctx(r.Context())

		event := finalLogger.Info()
		if rw.statusCode >= 500 || bag.hasGroup("error") {
			event = finalLogger.Error()
		} else if rw.statusCode >= 400 || bag.hasWarning() {
			event = finalLogger.Warn()
		}

		clientIP := r.RemoteAddr
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			clientIP = strings.TrimSpace(parts[0])
		} else if xri := r.Header.Get("X-Real-IP"); xri != "" {
			clientIP = strings.TrimSpace(xri)
		}

		path := r.URL.Path
		if r.URL.RawQuery != "" {
			path += "?" + r.URL.RawQuery
		}

		event.
			Int("status", rw.statusCode).
			Str("method", r.Method).
			Str("path", path).
			Str("ip", clientIP).
			Str("latency", stop.String()).
			Str("user-agent", r.UserAgent())

		bag.apply(event)
		event.Msg(bag.Message())
	})
}
