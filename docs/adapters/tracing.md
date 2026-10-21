# Distributed Tracing Middleware Recipes

`canonical-logger` does not hardcode a tracing vendor or header format in core. Instead, attach `traceId` and `spanId` to the active `FieldBag` via `clog.WithField` in a lightweight middleware matching your stack's trace standard.

Register your trace middleware **after** your canonical logger middleware (so a `FieldBag` is already present in `r.Context()`).

---

## Option A: OpenTelemetry (`go.opentelemetry.io/otel/trace`)

If your service uses OpenTelemetry, extract the `traceId` and `spanId` directly from the active span in `r.Context()`:

```go
package middleware

import (
	"net/http"

	"github.com/Shannor/canonical-logger"
	"go.opentelemetry.io/otel/trace"
)

func OTelTraceMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if sc := trace.SpanFromContext(ctx).SpanContext(); sc.IsValid() {
			ctx = clog.WithField(ctx, "traceId", sc.TraceID().String())
			ctx = clog.WithField(ctx, "spanId", sc.SpanID().String())
			r = r.WithContext(ctx)
		}
		next.ServeHTTP(w, r)
	})
}
```

---

## Option B: Standard W3C TraceContext (`traceparent`)

Zero-dependency parser for the W3C `traceparent` header (`00-<32-hex-trace-id>-<16-hex-span-id>-<flags>`) with a cryptographic 32-character hex fallback:

```go
package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/Shannor/canonical-logger"
)

func W3CTraceMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		traceID, spanID := extractW3CTrace(r.Header.Get("traceparent"))
		ctx := clog.WithField(r.Context(), "traceId", traceID)
		if spanID != "" {
			ctx = clog.WithField(ctx, "spanId", spanID)
		}
		w.Header().Set("X-Trace-Id", traceID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func extractW3CTrace(traceparent string) (traceID, spanID string) {
	// Format: version(2)-traceId(32)-spanId(16)-flags(2)
	parts := strings.Split(strings.TrimSpace(traceparent), "-")
	if len(parts) == 4 && len(parts[1]) == 32 && len(parts[2]) == 16 {
		return parts[1], parts[2]
	}
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b), ""
}
```

---

## Option C: Sentry (`sentry-trace` & `baggage`)

Extracts `traceId` and `spanId` from Sentry's `sentry-trace` header (`<trace_id>-<span_id>[-<sampled>]`), falls back to `baggage` (`sentry-trace_id=...`), and generates a random 32-char hex `traceId` when neither is present:

```go
package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/Shannor/canonical-logger"
)

const (
	HeaderSentryTrace   = "sentry-trace"
	HeaderBaggage       = "baggage"
	ResponseTraceHeader = "X-Trace-Id"
)

func SentryTraceMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		traceID, spanID := ExtractSentryTrace(
			r.Header.Get(HeaderSentryTrace),
			r.Header.Get(HeaderBaggage),
		)

		ctx := clog.WithField(r.Context(), "traceId", traceID)
		if spanID != "" {
			ctx = clog.WithField(ctx, "spanId", spanID)
		}

		w.Header().Set(ResponseTraceHeader, traceID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func ExtractSentryTrace(sentryTrace, baggage string) (traceID, spanID string) {
	if sentryTrace != "" {
		if id, span, found := strings.Cut(sentryTrace, "-"); found && len(id) == 32 {
			spanID, _, _ = strings.Cut(span, "-")
			return id, spanID
		}
	}

	for rest := baggage; rest != ""; {
		var pair string
		pair, rest, _ = strings.Cut(rest, ",")
		key, value, found := strings.Cut(strings.TrimSpace(pair), "=")
		if found && key == "sentry-trace_id" && value != "" {
			return value, ""
		}
	}

	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b), ""
}
```
