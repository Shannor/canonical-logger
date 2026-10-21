# Standard `net/http` Middleware Recipe (`ServeMux`, Chi, Gorilla Mux)

`clog.HTTPHandler` ships in the core package and works standalone out of the box (automatically opening a `FieldBag` and flushing on return when no middleware is installed).

If you want your canonical log line to include HTTP transport metadata (`status`, `method`, `path`, `ip`, `latency`, `user-agent`) and status-code severity escalation (`5xx` -> `ERROR`, `4xx` -> `WARN`), wrap your router with the `HTTPMiddleware` recipe below. Because `HTTPMiddleware` opens a `FieldBag` on `r.Context()` via `clog.NewContext`, `clog.HTTPHandler` (and `clog.Run`) will record the handler's outcome onto that bag and let `HTTPMiddleware` emit a single combined event at the end of the request.

---

## Recipe: `HTTPMiddleware`

```go
package middleware

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Shannor/canonical-logger"
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

// HTTPMiddleware implements the canonical log line pattern for any standard net/http router.
func HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ctx := clog.NewContext(r.Context())
		bag := clog.BagFromContext(ctx)
		r = r.WithContext(ctx)

		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(rw, r)

		stop := time.Since(start)
		logger := clog.LoggerFromContext(r.Context())

		level := slog.LevelInfo
		if rw.statusCode >= 500 || bag.HasGroup("error") {
			level = slog.LevelError
		} else if rw.statusCode >= 400 || bag.HasWarning() {
			level = slog.LevelWarn
		}

		clientIP := r.RemoteAddr
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			first, _, _ := strings.Cut(xff, ",")
			clientIP = strings.TrimSpace(first)
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
```

---

## Wiring with `http.ServeMux` and `clog.HTTPHandler`

```go
package main

import (
	"log/slog"
	"net/http"
	"os"

	"github.com/Shannor/canonical-logger"
	"your-module/internal/middleware"
)

func main() {
	slog.SetDefault(slog.New(clog.NewJSONHandler(os.Stdout, nil)))

	mux := http.NewServeMux()

	mux.Handle("GET /users/{id}", clog.HTTPHandler("get-user", func(w http.ResponseWriter, r *http.Request) error {
		clog.WithGroupField(r.Context(), "request", "userId", r.PathValue("id"))
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte(`{"status":"ok"}`))
		return err
	}))

	_ = http.ListenAndServe(":8080", middleware.HTTPMiddleware(mux))
}
```
