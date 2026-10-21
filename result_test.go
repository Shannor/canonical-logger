package clog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRecordResult_NilError verifies that a nil error sets message to "{baseEvent}-success".
func TestRecordResult_NilError(t *testing.T) {
	ctx := NewContext(context.Background())
	recordResult(ctx, "get-campaign", nil)
	assert.Equal(t, "get-campaign-success", BagFromContext(ctx).Message())
}

// TestRecordResult_WithError verifies that an error sets "{baseEvent}-failure" and the error group.
func TestRecordResult_WithError(t *testing.T) {
	ctx := NewContext(context.Background())
	recordResult(ctx, "get-campaign", errors.New("db error"))

	bag := BagFromContext(ctx)
	assert.Equal(t, "get-campaign-failure", bag.Message())
	assert.True(t, bag.hasGroup("error"))
	assert.Equal(t, "db error", bag.getGroup("error")["err"])
}

// TestRecordResult_NilBag verifies that recording on a context without a FieldBag is a no-op.
func TestRecordResult_NilBag(t *testing.T) {
	assert.NotPanics(t, func() { recordResult(context.Background(), "get-campaign", errors.New("x")) })
}

type codedError struct {
	msg      string
	code     string
	category string
	fields   map[string]any
	cause    error
}

func (e *codedError) Error() string               { return e.msg }
func (e *codedError) Code() string                { return e.code }
func (e *codedError) Category() string            { return e.category }
func (e *codedError) ErrorFields() map[string]any { return e.fields }
func (e *codedError) Unwrap() error               { return e.cause }

func TestDefaultErrorFormatter_ExtractsMetadataAndCause(t *testing.T) {
	rootErr := errors.New("connection refused")
	richErr := &codedError{
		msg:      "orderDAO.Insert: failed to connect",
		code:     "DB_CONN_ERR",
		category: "transient",
		fields:   map[string]any{"retryable": true},
		cause:    rootErr,
	}

	ctx := NewContext(context.Background())
	recordResult(ctx, "create-order", richErr)

	bag := BagFromContext(ctx)
	errGroup := bag.getGroup("error")
	require.NotNil(t, errGroup)
	assert.Equal(t, "orderDAO.Insert: failed to connect", errGroup["err"])
	assert.Equal(t, "DB_CONN_ERR", errGroup["code"])
	assert.Equal(t, "transient", errGroup["category"])
	assert.Equal(t, true, errGroup["retryable"])
	assert.Equal(t, "connection refused", errGroup["cause"])
}

func TestSetErrorFormatter_CustomAndReset(t *testing.T) {
	t.Cleanup(func() { SetErrorFormatter(nil) })

	SetErrorFormatter(func(err error) Fields {
		return Fields{
			"err":    err.Error(),
			"custom": "formatted",
		}
	})

	ctx := NewContext(context.Background())
	recordResult(ctx, "custom-op", errors.New("boom"))

	bag := BagFromContext(ctx)
	errGroup := bag.getGroup("error")
	require.NotNil(t, errGroup)
	assert.Equal(t, "boom", errGroup["err"])
	assert.Equal(t, "formatted", errGroup["custom"])

	// Reset to DefaultErrorFormatter
	SetErrorFormatter(nil)
	ctx2 := NewContext(context.Background())
	recordResult(ctx2, "default-op", errors.New("plain"))
	errGroup2 := BagFromContext(ctx2).getGroup("error")
	assert.Equal(t, "plain", errGroup2["err"])
	assert.NotContains(t, errGroup2, "custom")
}

// --- Return-value result flow (merged from result_flow_test.go) ---

var errProcess = errors.New("process failed")

func process(context.Context) (string, error) { return "", errProcess }

// TestRun_ShadowedErrorStillRecordsFailure is the regression test for the shadowing
// gotcha: inside Run the outcome comes from the return value, so := is harmless.
func TestRun_ShadowedErrorStillRecordsFailure(t *testing.T) {
	ctx := NewContext(context.Background())
	got := Run(ctx, "handle-action", func(ctx context.Context) error {
		if true {
			data, err := process(ctx) // := shadowing is harmless inside Run
			if err != nil {
				return err
			}
			_ = data
		}
		return nil
	})

	require.ErrorIs(t, got, errProcess)
	bag := BagFromContext(ctx)
	assert.Equal(t, "handle-action-failure", bag.Message())
	assert.Equal(t, "process failed", bag.getGroup("error")["err"])
}

func TestRun_Success(t *testing.T) {
	ctx := NewContext(context.Background())
	require.NoError(t, Run(ctx, "op", func(context.Context) error { return nil }))
	assert.Equal(t, "op-success", BagFromContext(ctx).Message())
}

func TestRun_PanicRecordsFailureAndRepanics(t *testing.T) {
	ctx := NewContext(context.Background())
	assert.PanicsWithValue(t, "kaboom", func() {
		_ = Run(ctx, "op", func(context.Context) error { panic("kaboom") })
	})
	bag := BagFromContext(ctx)
	assert.Equal(t, "op-failure", bag.Message())
	assert.Contains(t, bag.getGroup("error")["err"], "kaboom")
}

func TestRunValue(t *testing.T) {
	ctx := NewContext(context.Background())
	v, err := RunValue(ctx, "get-user", func(context.Context) (int, error) { return 42, nil })
	require.NoError(t, err)
	assert.Equal(t, 42, v)
	assert.Equal(t, "get-user-success", BagFromContext(ctx).Message())

	ctx2 := NewContext(context.Background())
	_, err = RunValue(ctx2, "get-user", func(ctx context.Context) (string, error) { return process(ctx) })
	require.ErrorIs(t, err, errProcess)
	assert.Equal(t, "get-user-failure", BagFromContext(ctx2).Message())
}

func TestRecordResult_FailureIsSticky(t *testing.T) {
	ctx := NewContext(context.Background())
	recordResult(ctx, "op", errProcess)
	recordResult(ctx, "op", nil)

	bag := BagFromContext(ctx)
	assert.Equal(t, "op-failure", bag.Message())
	assert.Equal(t, "process failed", bag.getGroup("error")["err"])
}

func TestRecordResult_NilErrorWithExistingErrorGroupIsFailure(t *testing.T) {
	ctx := NewContext(context.Background())
	// e.g. a router adapter attached framework errors to the "error" group.
	WithGroupField(ctx, "error", "err", "bind failed")
	recordResult(ctx, "op", nil)
	assert.Equal(t, "op-failure", BagFromContext(ctx).Message())
}

func TestHTTPHandler_RecordsReturnedError(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(NewJSONHandler(&buf, nil))

	h := HTTPHandler("handle-action", func(w http.ResponseWriter, r *http.Request) error {
		data, err := process(r.Context())
		if err != nil {
			http.Error(w, "internal", http.StatusInternalServerError)
			return err
		}
		_, _ = w.Write([]byte(data))
		return nil
	})

	// Simulate an outer transport middleware that opens a FieldBag and emits once on exit.
	srv := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := NewContext(NewContextWithLogger(r.Context(), logger))
		bag := BagFromContext(ctx)
		h.ServeHTTP(w, r.WithContext(ctx))
		LoggerFromContext(ctx).LogAttrs(ctx, slog.LevelError, bag.Message(), bag.Attrs()...)
	})

	srv.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/x", nil))

	var out map[string]any
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &out))
	assert.Equal(t, "handle-action-failure", out["message"])
	errGroup := out["error"].(map[string]any)
	assert.Equal(t, "process failed", errGroup["err"])
}

func TestRun_StandaloneFlushesAndReturnsError(t *testing.T) {
	var got error
	lines := captureBackground(t, func() {
		got = Run(context.Background(), "startup-migrations", func(ctx context.Context) error {
			WithGroupField(ctx, "db.migrate", "applied", 3)
			_, err := process(ctx)
			return err
		})
	})
	require.ErrorIs(t, got, errProcess)
	require.Len(t, lines, 1)
	assert.Equal(t, "startup-migrations", lines[0]["job"])
	assert.Equal(t, "startup-migrations-failure", lines[0]["message"])
	assert.Equal(t, "ERROR", lines[0]["level"])
	assert.NotEmpty(t, lines[0]["latency"])
	dbGrp := lines[0]["db"].(map[string]any)["migrate"].(map[string]any)
	assert.Equal(t, float64(3), dbGrp["applied"])
}

func TestRun_StandalonePanicFlushesThenPropagates(t *testing.T) {
	lines := captureBackground(t, func() {
		assert.Panics(t, func() {
			_ = Run(context.Background(), "boom-task", func(context.Context) error { panic("boom") })
		})
	})
	require.Len(t, lines, 1, "canonical line must still be flushed")
	assert.Equal(t, "boom-task-failure", lines[0]["message"])
}

func TestRunValue_StandaloneFlushes(t *testing.T) {
	var got int
	var gotErr error
	lines := captureBackground(t, func() {
		got, gotErr = RunValue(context.Background(), "get-user", func(ctx context.Context) (int, error) {
			WithGroupField(ctx, "request", "userId", "usr_1")
			return 42, nil
		})
	})
	require.NoError(t, gotErr)
	assert.Equal(t, 42, got)
	require.Len(t, lines, 1)
	assert.Equal(t, "get-user-success", lines[0]["message"])
	assert.Equal(t, "usr_1", lines[0]["request"].(map[string]any)["userId"])
}

func TestRunFunc_RecordsSuccess(t *testing.T) {
	lines := captureBackground(t, func() {
		RunFunc("sync", func(context.Context) error { return nil })()
	})
	require.Len(t, lines, 1)
	assert.Equal(t, "sync-success", lines[0]["message"])
}

func TestGo_RecordsReturnedError(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	lines := captureBackground(t, func() {
		Go(context.Background(), "send-email", func(ctx context.Context) error {
			defer wg.Done()
			_, err := process(ctx)
			return err
		})
		wg.Wait()
		goWait()
	})
	require.Len(t, lines, 1)
	assert.Equal(t, "send-email-failure", lines[0]["message"])
	assert.Equal(t, "process failed", lines[0]["error"].(map[string]any)["err"])
}

func TestRun_NestedOuterOwnsEventAndFailureIsSticky(t *testing.T) {
	ctx := NewContext(context.Background())
	_ = Run(ctx, "outer", func(ctx context.Context) error {
		_ = Run(ctx, "inner", func(context.Context) error { return errProcess })
		return nil // outer swallows the error, but the failure is sticky
	})
	assert.Equal(t, "inner-failure", BagFromContext(ctx).Message())

	ctx2 := NewContext(context.Background())
	_ = Run(ctx2, "outer", func(ctx context.Context) error {
		return Run(ctx, "inner", func(context.Context) error { return nil })
	})
	assert.Equal(t, "outer-success", BagFromContext(ctx2).Message())

	// Standalone nested Run without pre-existing bag flushes once at the outer boundary.
	lines := captureBackground(t, func() {
		_ = Run(context.Background(), "outer-standalone", func(ctx context.Context) error {
			return Run(ctx, "inner-standalone", func(ctx context.Context) error {
				WithField(ctx, "step", "done")
				return nil
			})
		})
	})
	require.Len(t, lines, 1)
	assert.Equal(t, "outer-standalone-success", lines[0]["message"])
	assert.Equal(t, "done", lines[0]["step"])
}

// TestHTTPHandler_WithoutMiddlewareStillFlushes verifies HTTPHandler works standalone:
// without a FieldBag on the request context, Run opens one and flushes the canonical
// log line when the handler returns.
func TestHTTPHandler_WithoutMiddlewareStillFlushes(t *testing.T) {
	h := HTTPHandler("no-bag", func(w http.ResponseWriter, r *http.Request) error {
		WithGroupField(r.Context(), "request", "tea", true)
		w.WriteHeader(http.StatusTeapot)
		return errProcess
	})
	rec := httptest.NewRecorder()
	lines := captureBackground(t, func() {
		assert.NotPanics(t, func() { h.ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil)) })
	})
	assert.Equal(t, http.StatusTeapot, rec.Code)
	require.Len(t, lines, 1)
	assert.Equal(t, "no-bag-failure", lines[0]["message"])
	assert.Equal(t, true, lines[0]["request"].(map[string]any)["tea"])
}
