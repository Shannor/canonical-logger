package clog

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupHTTPTraceServer(buf *bytes.Buffer, handler http.HandlerFunc) http.Handler {
	logger := slog.New(NewJSONHandler(buf, nil))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(NewContextWithLogger(r.Context(), logger))
		HTTPMiddleware(HTTPTraceMiddleware(handler)).ServeHTTP(w, r)
	})
}

func parseTraceLogOutput(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	var out map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &out), "log output: %s", buf.String())
	return out
}

func TestHTTPTraceMiddleware_ExtractsFromSentryTraceHeader(t *testing.T) {
	var buf bytes.Buffer

	srv := setupHTTPTraceServer(&buf, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set("sentry-trace", "827fddb6f17a434f9118dc7bca3e0f22-9c6e42da7199350c-0")
	srv.ServeHTTP(w, req)

	assert.Equal(t, "827fddb6f17a434f9118dc7bca3e0f22", w.Header().Get("X-Trace-Id"))

	out := parseTraceLogOutput(t, &buf)
	assert.Equal(t, "827fddb6f17a434f9118dc7bca3e0f22", out["traceId"])
	assert.Equal(t, "9c6e42da7199350c", out["spanId"])
}

func TestHTTPTraceMiddleware_ExtractsFromBaggageHeader(t *testing.T) {
	var buf bytes.Buffer

	srv := setupHTTPTraceServer(&buf, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set("baggage", "sentry-environment=dev,sentry-trace_id=abc123def456,sentry-sampled=false")
	srv.ServeHTTP(w, req)

	assert.Equal(t, "abc123def456", w.Header().Get("X-Trace-Id"))

	out := parseTraceLogOutput(t, &buf)
	assert.Equal(t, "abc123def456", out["traceId"])
}

func TestHTTPTraceMiddleware_SentryTraceTakesPrecedenceOverBaggage(t *testing.T) {
	var buf bytes.Buffer

	srv := setupHTTPTraceServer(&buf, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set("sentry-trace", "aaaa1111bbbb2222cccc3333dddd4444-9c6e42da7199350c-1")
	req.Header.Set("baggage", "sentry-trace_id=different-id,sentry-sampled=false")
	srv.ServeHTTP(w, req)

	assert.Equal(t, "aaaa1111bbbb2222cccc3333dddd4444", w.Header().Get("X-Trace-Id"))
}

func TestHTTPTraceMiddleware_GeneratesRandomWhenNoHeaders(t *testing.T) {
	var buf bytes.Buffer

	srv := setupHTTPTraceServer(&buf, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	srv.ServeHTTP(w, req)

	traceID := w.Header().Get("X-Trace-Id")
	assert.NotEmpty(t, traceID, "should generate a trace ID when no headers present")
	assert.Len(t, traceID, 32, "generated trace ID should be 32 hex chars")

	out := parseTraceLogOutput(t, &buf)
	assert.Equal(t, traceID, out["traceId"])
}

func TestHTTPTraceMiddleware_GeneratedIDsAreUnique(t *testing.T) {
	var buf1, buf2 bytes.Buffer

	srv1 := setupHTTPTraceServer(&buf1, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	srv2 := setupHTTPTraceServer(&buf2, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })

	w1 := httptest.NewRecorder()
	w2 := httptest.NewRecorder()
	req1, _ := http.NewRequest("GET", "/test", nil)
	req2, _ := http.NewRequest("GET", "/test", nil)

	srv1.ServeHTTP(w1, req1)
	srv2.ServeHTTP(w2, req2)

	id1 := w1.Header().Get("X-Trace-Id")
	id2 := w2.Header().Get("X-Trace-Id")
	assert.NotEqual(t, id1, id2, "each request should get a unique trace ID")
}

func TestHTTPTraceMiddleware_MalformedSentryTraceFallsToBaggage(t *testing.T) {
	var buf bytes.Buffer

	srv := setupHTTPTraceServer(&buf, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set("sentry-trace", "malformed-no-dashes-in-right-places")
	req.Header.Set("baggage", "sentry-trace_id=fallback-trace-id,sentry-sampled=false")
	srv.ServeHTTP(w, req)

	assert.Equal(t, "fallback-trace-id", w.Header().Get("X-Trace-Id"))
}
