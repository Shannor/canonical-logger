package cloggin

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Shannor/canonical-logger"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupMiddlewareRouter(buf *bytes.Buffer, handler gin.HandlerFunc, opts ...Option) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	logger := slog.New(clog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	r.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(clog.NewContextWithLogger(c.Request.Context(), logger))
		c.Next()
	})

	r.Use(Middleware(opts...))
	r.GET("/test", handler)
	return r
}

func setupTraceRouter(buf *bytes.Buffer, handler gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	logger := slog.New(clog.NewJSONHandler(buf, nil))
	r.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(clog.NewContextWithLogger(c.Request.Context(), logger))
		c.Next()
	})

	r.Use(Middleware())
	r.Use(TraceID())
	r.GET("/test", handler)
	return r
}

func parseLogLine(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	var out map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &out), "log output: %s", buf.String())
	return out
}

func TestMiddleware_FieldBagCreatedPerRequest(t *testing.T) {
	var gotBag *clog.FieldBag
	var buf bytes.Buffer

	r := setupMiddlewareRouter(&buf, func(c *gin.Context) {
		gotBag = clog.BagFromContext(c.Request.Context())
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NotNil(t, gotBag, "FieldBag must be present in request context")
}

func TestMiddleware_ContextResolverFallback_ReplacedRequestContext(t *testing.T) {
	var buf bytes.Buffer

	r := setupMiddlewareRouter(&buf, func(c *gin.Context) {
		// Simulate an auth or OpenAPI middleware replacing c.Request with a fresh context
		// that only embeds gin.ContextKey.
		freshCtx := context.WithValue(context.Background(), gin.ContextKey, c)
		clog.WithGroupField(freshCtx, "actor", "requestorId", "usr-resolved")
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	out := parseLogLine(t, &buf)
	actor, ok := out["actor"].(map[string]any)
	require.True(t, ok, "expected actor group resolved via Gin ContextResolver")
	assert.Equal(t, "usr-resolved", actor["requestorId"])
}

func TestMiddleware_HandlerFieldsAppearedInCanonicalLine(t *testing.T) {
	var buf bytes.Buffer

	r := setupMiddlewareRouter(&buf, func(c *gin.Context) {
		ctx := clog.WithField(c.Request.Context(), "custom_key", "custom_value")
		c.Request = c.Request.WithContext(ctx)
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	out := parseLogLine(t, &buf)
	assert.Equal(t, "custom_value", out["custom_key"])
}

func TestMiddleware_GroupFieldsAppearedInCanonicalLine(t *testing.T) {
	var buf bytes.Buffer

	r := setupMiddlewareRouter(&buf, func(c *gin.Context) {
		ctx := clog.WithGroupField(c.Request.Context(), "user", "id", "usr-999")
		ctx = clog.WithGroupField(ctx, "user", "is_admin", true)
		c.Request = c.Request.WithContext(ctx)
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	out := parseLogLine(t, &buf)
	userGroup, ok := out["user"].(map[string]any)
	require.True(t, ok, "expected 'user' to be a JSON object, got: %v", out["user"])
	assert.Equal(t, "usr-999", userGroup["id"])
	assert.Equal(t, true, userGroup["is_admin"])
}

func TestMiddleware_HTTPFieldsStillPresent(t *testing.T) {
	var buf bytes.Buffer

	r := setupMiddlewareRouter(&buf, func(c *gin.Context) {
		ctx := clog.WithField(c.Request.Context(), "extra", "value")
		c.Request = c.Request.WithContext(ctx)
		c.Status(http.StatusCreated)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	req.Header.Set("User-Agent", "test-agent/1.0")
	r.ServeHTTP(w, req)

	out := parseLogLine(t, &buf)
	assert.EqualValues(t, float64(201), out["status"])
	assert.Equal(t, "GET", out["method"])
	assert.Equal(t, "/test", out["path"])
	assert.NotEmpty(t, out["latency"])
	assert.Contains(t, out, "ip")
	assert.Equal(t, "test-agent/1.0", out["user-agent"])
	assert.Equal(t, "value", out["extra"])
}

func TestMiddleware_WarningAndErrorEscalation(t *testing.T) {
	var buf bytes.Buffer

	r := setupMiddlewareRouter(&buf, func(c *gin.Context) {
		ctx := clog.WithField(c.Request.Context(), "warning", "something suspicious")
		ctx = clog.WithGroupField(ctx, "error", "msg", "critical failure")
		c.Request = c.Request.WithContext(ctx)
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	out := parseLogLine(t, &buf)
	assert.Equal(t, "ERROR", out["level"])
}

func TestMiddleware_SensitiveParamMasking(t *testing.T) {
	var buf bytes.Buffer
	gin.SetMode(gin.TestMode)
	r := gin.New()

	logger := slog.New(clog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	r.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(clog.NewContextWithLogger(c.Request.Context(), logger))
		c.Next()
	})
	r.Use(GinMiddleware(WithSensitiveParams("customSecret")))

	r.GET("/users/:token", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	r.GET("/items/:customSecret", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	w1 := httptest.NewRecorder()
	req1, _ := http.NewRequest("GET", "/users/super-secret-token", nil)
	r.ServeHTTP(w1, req1)

	out1 := parseLogLine(t, &buf)
	assert.Equal(t, "/users/:token", out1["path"])

	buf.Reset()

	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("GET", "/items/secret-item-id", nil)
	r.ServeHTTP(w2, req2)

	out2 := parseLogLine(t, &buf)
	assert.Equal(t, "/items/:customSecret", out2["path"])
}

func TestTraceID_SentryTraceAndBaggage(t *testing.T) {
	var buf bytes.Buffer

	r := setupTraceRouter(&buf, func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set("sentry-trace", "827fddb6f17a434f9118dc7bca3e0f22-9c6e42da7199350c-0")
	r.ServeHTTP(w, req)

	assert.Equal(t, "827fddb6f17a434f9118dc7bca3e0f22", w.Header().Get("X-Trace-Id"))

	out := parseLogLine(t, &buf)
	assert.Equal(t, "827fddb6f17a434f9118dc7bca3e0f22", out["traceId"])
	assert.Equal(t, "9c6e42da7199350c", out["spanId"])
}
