package clog

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const (
	// HeaderSentryTrace is the W3C-style Sentry trace propagation header.
	// Format: <trace_id>-<span_id>[-<sampled>]
	HeaderSentryTrace = "sentry-trace"

	// HeaderBaggage is the W3C baggage header that may contain sentry-trace_id.
	HeaderBaggage = "baggage"

	// ResponseTraceHeader is the response header name used to return the trace ID.
	ResponseTraceHeader = "X-Trace-Id"
)

// TraceID returns a gin middleware that extracts a trace ID from incoming Sentry
// headers (sentry-trace or baggage) and adds it to the xlog context for the
// canonical log line. If no trace headers are present, a random 32-char hex
// trace ID is generated.
//
// The trace ID is also set as a response header (X-Trace-Id) on every response,
// regardless of success or failure.
//
// This middleware must be registered AFTER GinMiddleware() so the FieldBag
// is available in the context.
func TraceID() gin.HandlerFunc {
	return func(c *gin.Context) {
		traceID, spanID := extractTrace(c.GetHeader(HeaderSentryTrace), c.GetHeader(HeaderBaggage))

		ctx := c.Request.Context()
		ctx = WithField(ctx, "traceId", traceID)
		if spanID != "" {
			ctx = WithField(ctx, "spanId", spanID)
		}
		c.Request = c.Request.WithContext(ctx)

		c.Header(ResponseTraceHeader, traceID)
		c.Next()
	}
}

// HTTPTraceMiddleware returns a standard net/http middleware that extracts or generates
// trace IDs and injects them into the canonical log context and response headers.
// Must be registered AFTER HTTPMiddleware so the FieldBag is present in context.
func HTTPTraceMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		traceID, spanID := extractTrace(r.Header.Get(HeaderSentryTrace), r.Header.Get(HeaderBaggage))

		ctx := r.Context()
		ctx = WithField(ctx, "traceId", traceID)
		if spanID != "" {
			ctx = WithField(ctx, "spanId", spanID)
		}
		r = r.WithContext(ctx)

		w.Header().Set(ResponseTraceHeader, traceID)
		next.ServeHTTP(w, r)
	})
}

// extractTrace attempts to extract a trace ID (and optionally span ID) from
// header values. It checks sentry-trace first, then baggage, and falls back
// to generating a random 32-char hex trace ID.
func extractTrace(sentryTrace, baggage string) (traceID, spanID string) {
	if sentryTrace != "" {
		traceID, spanID = parseSentryTrace(sentryTrace)
		if traceID != "" {
			return traceID, spanID
		}
	}

	if baggage != "" {
		traceID = parseTraceIDFromBaggage(baggage)
		if traceID != "" {
			return traceID, ""
		}
	}

	return generateTraceID(), ""
}

// parseSentryTrace parses the sentry-trace header format: <trace_id>-<span_id>[-<sampled>]
// Returns empty strings if the format is invalid.
func parseSentryTrace(header string) (traceID, spanID string) {
	parts := strings.SplitN(header, "-", 3)
	if len(parts) < 2 {
		return "", ""
	}

	traceID = parts[0]
	spanID = parts[1]

	// Basic validation: trace ID should be a 32-char hex string
	if len(traceID) != 32 {
		return "", ""
	}

	return traceID, spanID
}

// parseTraceIDFromBaggage extracts sentry-trace_id from a W3C baggage header.
// Baggage format: key1=value1,key2=value2,...
func parseTraceIDFromBaggage(baggage string) string {
	for _, pair := range strings.Split(baggage, ",") {
		pair = strings.TrimSpace(pair)
		key, value, found := strings.Cut(pair, "=")
		if found && key == "sentry-trace_id" {
			return value
		}
	}
	return ""
}

// generateTraceID produces a cryptographically random 32-character hex string,
// matching the format of a standard trace ID.
func generateTraceID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "00000000000000000000000000000000"
	}
	return hex.EncodeToString(b)
}
