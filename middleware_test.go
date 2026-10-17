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

func setupHTTPServer(buf *bytes.Buffer, handler http.HandlerFunc) http.Handler {
	logger := slog.New(NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(NewContextWithLogger(r.Context(), logger))
		HTTPMiddleware(handler).ServeHTTP(w, r)
	})
}

func parseLogLine(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	var out map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &out), "log output: %s", buf.String())
	return out
}

func TestHTTPMiddleware_FieldBagCreatedPerRequest(t *testing.T) {
	var gotBag *FieldBag
	var buf bytes.Buffer

	srv := setupHTTPServer(&buf, func(w http.ResponseWriter, r *http.Request) {
		gotBag = BagFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	srv.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NotNil(t, gotBag, "FieldBag must be present in request context")
}

func TestHTTPMiddleware(t *testing.T) {
	var buf bytes.Buffer

	srv := setupHTTPServer(&buf, func(w http.ResponseWriter, r *http.Request) {
		ctx := WithField(r.Context(), "app_version", "v1.2.3")
		ctx = WithGroupField(ctx, "user", "id", "usr-42")
		*r = *r.WithContext(ctx)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("ok"))
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/v1/resource?query=1", nil)
	req.RemoteAddr = "10.0.0.1:54321"
	req.Header.Set("User-Agent", "go-client/1.0")
	srv.ServeHTTP(w, req)

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

	srv := setupHTTPServer(&buf, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/fail", nil)
	srv.ServeHTTP(w, req)

	out := parseLogLine(t, &buf)
	assert.Equal(t, "ERROR", out["level"])
}

func TestHTTPMiddleware_WarningStatus(t *testing.T) {
	var buf bytes.Buffer

	srv := setupHTTPServer(&buf, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/not-found", nil)
	srv.ServeHTTP(w, req)

	out := parseLogLine(t, &buf)
	assert.Equal(t, "WARN", out["level"])
}

func TestHTTPMiddleware_WarningFieldEscalation(t *testing.T) {
	var buf bytes.Buffer

	srv := setupHTTPServer(&buf, func(w http.ResponseWriter, r *http.Request) {
		WithField(r.Context(), "warning", "degraded upstream")
		w.WriteHeader(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/ok-with-warn", nil)
	srv.ServeHTTP(w, req)

	out := parseLogLine(t, &buf)
	assert.Equal(t, "WARN", out["level"])
}

func TestHTTPMiddleware_ErrorGroupEscalation(t *testing.T) {
	var buf bytes.Buffer

	srv := setupHTTPServer(&buf, func(w http.ResponseWriter, r *http.Request) {
		WithGroupField(r.Context(), "error", "err", "domain failure")
		w.WriteHeader(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/ok-with-err", nil)
	srv.ServeHTTP(w, req)

	out := parseLogLine(t, &buf)
	assert.Equal(t, "ERROR", out["level"])
}

func TestHTTPMiddleware_ForwardedIPHeaders(t *testing.T) {
	var buf bytes.Buffer

	srv := setupHTTPServer(&buf, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("implicit 200"))
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/ip", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.5, 10.0.0.1")
	srv.ServeHTTP(w, req)

	out := parseLogLine(t, &buf)
	assert.Equal(t, "203.0.113.5", out["ip"])
	assert.Equal(t, float64(200), out["status"])
}
