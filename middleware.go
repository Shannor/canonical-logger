package clog

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
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

// GinMiddleware returns a gin.HandlerFunc that logs requests using clog and log/slog.
// It implements the canonical log line pattern by:
// 1. Creating a FieldBag per request and storing it in the request context.
// 2. After c.Next(), merging all accumulated fields (root + group attrs) into the canonical log event.
func GinMiddleware(opts ...GinMiddlewareOption) gin.HandlerFunc {
	cfg := &ginMiddlewareConfig{
		sensitiveParams: []string{"token", "optInID"},
	}
	for _, opt := range opts {
		opt(cfg)
	}

	return func(c *gin.Context) {
		start := time.Now()
		ctx := c.Request.Context()
		bag := &FieldBag{logger: rawLoggerFromContext(ctx)}
		ctx = context.WithValue(ctx, fieldBagCtxKey{}, bag)
		c.Set(ginFieldBagKey, bag)
		c.Request = c.Request.WithContext(ctx)

		// Process request
		c.Next()

		// Log canonical line only after request is processed
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

		logger := LoggerFromContext(c.Request.Context())

		level := slog.LevelInfo
		var ginErrAttr slog.Attr
		hasGinErr := false
		if len(c.Errors) > 0 || bag.hasGroup("error") {
			level = slog.LevelError
			if len(c.Errors) > 0 {
				ginErrAttr = slog.String("gin_error", c.Errors.String())
				hasGinErr = true
			}
		} else if bag.hasWarning() {
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
		ctx := r.Context()
		bag := &FieldBag{logger: rawLoggerFromContext(ctx)}
		ctx = context.WithValue(ctx, fieldBagCtxKey{}, bag)
		r = r.WithContext(ctx)

		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(rw, r)

		stop := time.Since(start)
		logger := LoggerFromContext(r.Context())

		level := slog.LevelInfo
		if rw.statusCode >= 500 || bag.hasGroup("error") {
			level = slog.LevelError
		} else if rw.statusCode >= 400 || bag.hasWarning() {
			level = slog.LevelWarn
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

		attrs := []slog.Attr{
			slog.Int("status", rw.statusCode),
			slog.String("method", r.Method),
			slog.String("path", path),
			slog.String("ip", clientIP),
			slog.String("latency", stop.String()),
			slog.String("user-agent", r.UserAgent()),
		}
		attrs = append(attrs, bag.Attrs()...)

		logger.LogAttrs(r.Context(), level, bag.Message(), attrs...)
	})
}
