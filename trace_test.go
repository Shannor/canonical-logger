package clog

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupTraceRouter builds a gin engine with GinMiddleware + TraceID middleware
// and a custom logger writing to buf for log inspection.
func setupTraceRouter(buf *bytes.Buffer, handler gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	logger := zerolog.New(buf)
	r.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(logger.WithContext(c.Request.Context()))
		c.Next()
	})

	r.Use(GinMiddleware())
	r.Use(TraceID())
	r.GET("/test", handler)
	return r
}

func parseTraceLogOutput(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	var out map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &out), "log output: %s", buf.String())
	return out
}

// TestTraceID_ExtractsFromSentryTraceHeader verifies the trace ID is extracted
// from the sentry-trace header when present.
func TestTraceID_ExtractsFromSentryTraceHeader(t *testing.T) {
	var buf bytes.Buffer

	r := setupTraceRouter(&buf, func(c *gin.Context) {
		c.Status(200)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set("sentry-trace", "827fddb6f17a434f9118dc7bca3e0f22-9c6e42da7199350c-0")
	r.ServeHTTP(w, req)

	assert.Equal(t, "827fddb6f17a434f9118dc7bca3e0f22", w.Header().Get("X-Trace-Id"))

	out := parseTraceLogOutput(t, &buf)
	assert.Equal(t, "827fddb6f17a434f9118dc7bca3e0f22", out["traceId"])
}

// TestTraceID_ExtractsFromBaggageHeader verifies the trace ID is extracted
// from the baggage header's sentry-trace_id value when sentry-trace is absent.
func TestTraceID_ExtractsFromBaggageHeader(t *testing.T) {
	var buf bytes.Buffer

	r := setupTraceRouter(&buf, func(c *gin.Context) {
		c.Status(200)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set("baggage", "sentry-environment=dev,sentry-trace_id=abc123def456,sentry-sampled=false")
	r.ServeHTTP(w, req)

	assert.Equal(t, "abc123def456", w.Header().Get("X-Trace-Id"))

	out := parseTraceLogOutput(t, &buf)
	assert.Equal(t, "abc123def456", out["traceId"])
}

// TestTraceID_SentryTraceHeaderTakesPrecedenceOverBaggage verifies that when both
// sentry-trace and baggage headers are present, sentry-trace is used.
func TestTraceID_SentryTraceHeaderTakesPrecedenceOverBaggage(t *testing.T) {
	var buf bytes.Buffer

	r := setupTraceRouter(&buf, func(c *gin.Context) {
		c.Status(200)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set("sentry-trace", "aaaa1111bbbb2222cccc3333dddd4444-9c6e42da7199350c-1")
	req.Header.Set("baggage", "sentry-trace_id=different-id,sentry-sampled=false")
	r.ServeHTTP(w, req)

	assert.Equal(t, "aaaa1111bbbb2222cccc3333dddd4444", w.Header().Get("X-Trace-Id"))
}

// TestTraceID_GeneratesRandomWhenNoHeaders verifies a random trace ID is generated
// when neither sentry-trace nor baggage headers are present.
func TestTraceID_GeneratesRandomWhenNoHeaders(t *testing.T) {
	var buf bytes.Buffer

	r := setupTraceRouter(&buf, func(c *gin.Context) {
		c.Status(200)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	traceID := w.Header().Get("X-Trace-Id")
	assert.NotEmpty(t, traceID, "should generate a trace ID when no headers present")
	assert.Len(t, traceID, 32, "generated trace ID should be 32 hex chars")

	out := parseTraceLogOutput(t, &buf)
	assert.Equal(t, traceID, out["traceId"])
}

// TestTraceID_GeneratedIDsAreUnique verifies that each request gets a unique trace ID.
func TestTraceID_GeneratedIDsAreUnique(t *testing.T) {
	var buf1, buf2 bytes.Buffer

	r1 := setupTraceRouter(&buf1, func(c *gin.Context) { c.Status(200) })
	r2 := setupTraceRouter(&buf2, func(c *gin.Context) { c.Status(200) })

	w1 := httptest.NewRecorder()
	w2 := httptest.NewRecorder()
	req1, _ := http.NewRequest("GET", "/test", nil)
	req2, _ := http.NewRequest("GET", "/test", nil)

	r1.ServeHTTP(w1, req1)
	r2.ServeHTTP(w2, req2)

	id1 := w1.Header().Get("X-Trace-Id")
	id2 := w2.Header().Get("X-Trace-Id")
	assert.NotEqual(t, id1, id2, "each request should get a unique trace ID")
}

// TestTraceID_ResponseHeaderOnError verifies the trace ID response header is set
// even when the handler returns an error status.
func TestTraceID_ResponseHeaderOnError(t *testing.T) {
	var buf bytes.Buffer

	r := setupTraceRouter(&buf, func(c *gin.Context) {
		c.Status(500)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set("sentry-trace", "aabb0011ccdd2233eeff4455aabb0011-abcdef123456-0")
	r.ServeHTTP(w, req)

	assert.Equal(t, "aabb0011ccdd2233eeff4455aabb0011", w.Header().Get("X-Trace-Id"))
}

// TestTraceID_MalformedSentryTraceHeaderFallsToBaggage verifies that a malformed
// sentry-trace header (missing dashes) falls back to baggage.
func TestTraceID_MalformedSentryTraceHeaderFallsToBaggage(t *testing.T) {
	var buf bytes.Buffer

	r := setupTraceRouter(&buf, func(c *gin.Context) {
		c.Status(200)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set("sentry-trace", "malformed-no-dashes-in-right-places")
	req.Header.Set("baggage", "sentry-trace_id=fallback-trace-id,sentry-sampled=false")
	r.ServeHTTP(w, req)

	assert.Equal(t, "fallback-trace-id", w.Header().Get("X-Trace-Id"))
}

// TestTraceID_SpanIdExtracted verifies the span ID from sentry-trace is also logged.
func TestTraceID_SpanIdExtracted(t *testing.T) {
	var buf bytes.Buffer

	r := setupTraceRouter(&buf, func(c *gin.Context) {
		c.Status(200)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set("sentry-trace", "827fddb6f17a434f9118dc7bca3e0f22-9c6e42da7199350c-0")
	r.ServeHTTP(w, req)

	out := parseTraceLogOutput(t, &buf)
	assert.Equal(t, "9c6e42da7199350c", out["spanId"])
}

// TestHTTPTraceMiddleware_ExtractsFromSentryTraceHeader verifies standard net/http trace middleware.
func TestHTTPTraceMiddleware_ExtractsFromSentryTraceHeader(t *testing.T) {
	var buf bytes.Buffer
	logger := zerolog.New(&buf)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(logger.WithContext(r.Context()))
		HTTPMiddleware(HTTPTraceMiddleware(handler)).ServeHTTP(w, r)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set("sentry-trace", "827fddb6f17a434f9118dc7bca3e0f22-9c6e42da7199350c-0")
	mw.ServeHTTP(w, req)

	assert.Equal(t, "827fddb6f17a434f9118dc7bca3e0f22", w.Header().Get("X-Trace-Id"))
	out := parseTraceLogOutput(t, &buf)
	assert.Equal(t, "827fddb6f17a434f9118dc7bca3e0f22", out["traceId"])
	assert.Equal(t, "9c6e42da7199350c", out["spanId"])
}
