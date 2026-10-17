package clog

import (
	"log/slog"
	"net/http"
	"strings"
	"time"
)

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
		ctx := NewContext(r.Context())
		bag := BagFromContext(ctx)
		r = r.WithContext(ctx)

		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(rw, r)

		stop := time.Since(start)
		logger := LoggerFromContext(r.Context())

		level := slog.LevelInfo
		if rw.statusCode >= 500 || bag.HasGroup("error") {
			level = slog.LevelError
		} else if rw.statusCode >= 400 || bag.HasWarning() {
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
