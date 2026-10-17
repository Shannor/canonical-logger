package clog

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
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

// HTTPTraceMiddleware returns a standard net/http middleware that extracts or generates
// trace IDs and injects them into the canonical log context and response headers.
// Must be registered AFTER HTTPMiddleware so the FieldBag is present in context.
func HTTPTraceMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		traceID, spanID := ExtractTrace(r.Header.Get(HeaderSentryTrace), r.Header.Get(HeaderBaggage))

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

// ExtractTrace extracts a trace ID (and optionally span ID) from incoming trace
// header values. It checks sentry-trace first, then baggage, and falls back
// to generating a cryptographically random 32-char hex trace ID.
func ExtractTrace(sentryTrace, baggage string) (traceID, spanID string) {
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

func extractTrace(sentryTrace, baggage string) (traceID, spanID string) {
	return ExtractTrace(sentryTrace, baggage)
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
