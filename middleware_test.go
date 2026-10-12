package clog

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupMiddlewareRouter builds a gin engine with GinMiddleware and a custom logger
// that writes to buf so we can inspect the canonical log line output.
func setupMiddlewareRouter(buf *bytes.Buffer, handler gin.HandlerFunc, opts ...GinMiddlewareOption) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	logger := slog.New(NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	r.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(NewContextWithLogger(c.Request.Context(), logger))
		c.Next()
	})

	r.Use(GinMiddleware(opts...))
	r.GET("/test", handler)
	return r
}

func parseLogLine(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	var out map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &out), "log output: %s", buf.String())
	return out
}

// TestMiddleware_FieldBagCreatedPerRequest verifies a FieldBag is injected into context.
func TestMiddleware_FieldBagCreatedPerRequest(t *testing.T) {
	var gotBag *FieldBag
	var buf bytes.Buffer

	r := setupMiddlewareRouter(&buf, func(c *gin.Context) {
		gotBag = BagFromContext(c.Request.Context())
		c.Status(200)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)
	assert.NotNil(t, gotBag, "FieldBag must be present in request context")
}

// TestMiddleware_HandlerFieldsAppearedInCanonicalLine verifies that fields set via
// WithField in a handler appear in the emitted canonical log line.
func TestMiddleware_HandlerFieldsAppearedInCanonicalLine(t *testing.T) {
	var buf bytes.Buffer

	r := setupMiddlewareRouter(&buf, func(c *gin.Context) {
		ctx := WithField(c.Request.Context(), "custom_key", "custom_value")
		c.Request = c.Request.WithContext(ctx)
		c.Status(200)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	out := parseLogLine(t, &buf)
	assert.Equal(t, "custom_value", out["custom_key"])
}

// TestMiddleware_GroupFieldsAppearedInCanonicalLine verifies group fields appear as
// nested JSON objects in the canonical log line.
func TestMiddleware_GroupFieldsAppearedInCanonicalLine(t *testing.T) {
	var buf bytes.Buffer

	r := setupMiddlewareRouter(&buf, func(c *gin.Context) {
		ctx := WithGroupField(c.Request.Context(), "user", "id", "usr-999")
		ctx = WithGroupField(ctx, "user", "is_admin", true)
		c.Request = c.Request.WithContext(ctx)
		c.Status(200)
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

// TestMiddleware_HTTPFieldsStillPresent verifies the canonical line still includes all
// existing HTTP-level fields alongside accumulated FieldBag fields.
func TestMiddleware_HTTPFieldsStillPresent(t *testing.T) {
	var buf bytes.Buffer

	r := setupMiddlewareRouter(&buf, func(c *gin.Context) {
		ctx := WithField(c.Request.Context(), "extra", "value")
		c.Request = c.Request.WithContext(ctx)
		c.Status(201)
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

// TestMiddleware_CanonicalMessageDefault verifies the canonical log message is "canonical-log-line".
func TestMiddleware_CanonicalMessageDefault(t *testing.T) {
	var buf bytes.Buffer

	r := setupMiddlewareRouter(&buf, func(c *gin.Context) {
		c.Status(200)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	out := parseLogLine(t, &buf)
	assert.Equal(t, "canonical-log-line", out["message"])
}

// TestMiddleware_CanonicalMessageOverride verifies bag.setMessage / WithMessage overrides the log message.
func TestMiddleware_CanonicalMessageOverride(t *testing.T) {
	var buf bytes.Buffer

	r := setupMiddlewareRouter(&buf, func(c *gin.Context) {
		WithMessage(c.Request.Context(), "my-operation")
		c.Status(200)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	out := parseLogLine(t, &buf)
	assert.Equal(t, "my-operation", out["message"])
}

func TestMiddleware_WarningLevel_RootField(t *testing.T) {
	var buf bytes.Buffer

	r := setupMiddlewareRouter(&buf, func(c *gin.Context) {
		ctx := WithField(c.Request.Context(), "warning", "something suspicious but okay")
		c.Request = c.Request.WithContext(ctx)
		c.Status(200)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	out := parseLogLine(t, &buf)
	assert.Equal(t, "WARN", out["level"])
}

func TestMiddleware_WarningLevel_GroupWarning(t *testing.T) {
	var buf bytes.Buffer

	r := setupMiddlewareRouter(&buf, func(c *gin.Context) {
		ctx := WithGroupField(c.Request.Context(), "warning", "msg", "something is not right")
		c.Request = c.Request.WithContext(ctx)
		c.Status(200)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	out := parseLogLine(t, &buf)
	assert.Equal(t, "WARN", out["level"])
}

func TestMiddleware_WarningLevel_NestedWarning(t *testing.T) {
	var buf bytes.Buffer

	r := setupMiddlewareRouter(&buf, func(c *gin.Context) {
		ctx := WithGroupField(c.Request.Context(), "someService.someMethod", "warning", "unhealthy dependency")
		c.Request = c.Request.WithContext(ctx)
		c.Status(200)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	out := parseLogLine(t, &buf)
	assert.Equal(t, "WARN", out["level"])
}

func TestMiddleware_ErrorTakesPrecedenceOverWarning(t *testing.T) {
	var buf bytes.Buffer

	r := setupMiddlewareRouter(&buf, func(c *gin.Context) {
		ctx := WithField(c.Request.Context(), "warning", "something suspicious")
		ctx = WithGroupField(ctx, "error", "msg", "critical failure")
		c.Request = c.Request.WithContext(ctx)
		c.Status(200)
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

	logger := slog.New(NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	r.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(NewContextWithLogger(c.Request.Context(), logger))
		c.Next()
	})
	r.Use(GinMiddleware(WithSensitiveParams("customSecret")))

	r.GET("/users/:token", func(c *gin.Context) {
		c.Status(200)
	})
	r.GET("/items/:customSecret", func(c *gin.Context) {
		c.Status(200)
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

// --- Standard net/http HTTPMiddleware tests ---

func TestHTTPMiddleware(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := WithField(r.Context(), "app_version", "v1.2.3")
		ctx = WithGroupField(ctx, "user", "id", "usr-42")
		*r = *r.WithContext(ctx)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("ok"))
	})

	mw := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(NewContextWithLogger(r.Context(), logger))
		HTTPMiddleware(handler).ServeHTTP(w, r)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/v1/resource?query=1", nil)
	req.RemoteAddr = "10.0.0.1:54321"
	req.Header.Set("User-Agent", "go-client/1.0")
	mw.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.Equal(t, "ok", w.Body.String())

	out := parseLogLine(t, &buf)
	assert.Equal(t, "POST", out["method"])
	assert.Equal(t, "/api/v1/resource?query=1", out["path"])
	assert.Equal(t, float64(201), out["status"])
	assert.Equal(t, "v1.2.3", out["app_version"])
	assert.Equal(t, "go-client/1.0", out["user-agent"])

	userGroup, ok := out["user"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "usr-42", userGroup["id"])
}

func TestHTTPMiddleware_ErrorStatus(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	mw := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(NewContextWithLogger(r.Context(), logger))
		HTTPMiddleware(handler).ServeHTTP(w, r)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/fail", nil)
	mw.ServeHTTP(w, req)

	out := parseLogLine(t, &buf)
	assert.Equal(t, "ERROR", out["level"])
}

func TestHTTPMiddleware_WarningStatus(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	mw := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(NewContextWithLogger(r.Context(), logger))
		HTTPMiddleware(handler).ServeHTTP(w, r)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/not-found", nil)
	mw.ServeHTTP(w, req)

	out := parseLogLine(t, &buf)
	assert.Equal(t, "WARN", out["level"])
}
