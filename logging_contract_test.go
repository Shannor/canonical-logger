package clog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLoggingContract_APIEquivalence verifies that accumulating canonical log fields via
// WithField/WithGroupField, WithFields/WithGroupFields, and WithAttrs/WithGroupAttrs
// produces the exact same JSON structure with no lost or corrupted keys across all group depths.
func TestLoggingContract_APIEquivalence(t *testing.T) {
	populateViaSingleFields := func(ctx context.Context) {
		WithField(ctx, "traceId", "d4cda95b652f4a1592b449d5929fda1b")
		WithField(ctx, "spanId", "6e0c63257de34c92")
		WithField(ctx, "attempt", 2)
		WithField(ctx, "cached", false)

		WithGroupField(ctx, "actor", "userId", "usr_123")
		WithGroupField(ctx, "actor", "tier", "premium")

		WithGroupField(ctx, "orderService.checkout", "orderId", "ord_789")
		WithGroupField(ctx, "orderService.checkout", "totalCents", int64(4999))

		WithGroupField(ctx, "orderService.validate", "valid", true)

		WithGroupField(ctx, "db.orders.insert", "table", "orders")
		WithGroupField(ctx, "db.orders.insert", "durationMs", int64(4))

		recordResult(ctx, "checkout-order", nil)
	}

	populateViaMapFields := func(ctx context.Context) {
		WithFields(ctx, Fields{
			"traceId": "d4cda95b652f4a1592b449d5929fda1b",
			"spanId":  "6e0c63257de34c92",
			"attempt": 2,
			"cached":  false,
		})
		WithGroupFields(ctx, "actor", Fields{
			"userId": "usr_123",
			"tier":   "premium",
		})
		WithGroupFields(ctx, "orderService.checkout", Fields{
			"orderId":    "ord_789",
			"totalCents": int64(4999),
		})
		WithGroupFields(ctx, "orderService.validate", Fields{
			"valid": true,
		})
		WithGroupFields(ctx, "db.orders.insert", Fields{
			"table":      "orders",
			"durationMs": int64(4),
		})
		recordResult(ctx, "checkout-order", nil)
	}

	populateViaAttrs := func(ctx context.Context) {
		WithAttrs(ctx,
			slog.String("traceId", "d4cda95b652f4a1592b449d5929fda1b"),
			slog.String("spanId", "6e0c63257de34c92"),
			slog.Int("attempt", 2),
			slog.Bool("cached", false),
		)
		WithGroupAttrs(ctx, "actor",
			slog.String("userId", "usr_123"),
			slog.String("tier", "premium"),
		)
		WithGroupAttrs(ctx, "orderService.checkout",
			slog.String("orderId", "ord_789"),
			slog.Int64("totalCents", 4999),
		)
		WithGroupAttrs(ctx, "orderService.validate",
			slog.Bool("valid", true),
		)
		WithGroupAttrs(ctx, "db.orders.insert",
			slog.String("table", "orders"),
			slog.Int64("durationMs", 4),
		)
		recordResult(ctx, "checkout-order", nil)
	}

	ctxSingle := NewContext(context.Background())
	populateViaSingleFields(ctxSingle)
	outSingle := captureEvent(t, BagFromContext(ctxSingle))

	ctxMap := NewContext(context.Background())
	populateViaMapFields(ctxMap)
	outMap := captureEvent(t, BagFromContext(ctxMap))

	ctxAttrs := NewContext(context.Background())
	populateViaAttrs(ctxAttrs)
	outAttrs := captureEvent(t, BagFromContext(ctxAttrs))

	// Remove dynamic timestamp before comparing full JSON maps
	delete(outSingle, "time")
	delete(outMap, "time")
	delete(outAttrs, "time")

	assert.Equal(t, outSingle, outMap, "WithFields/WithGroupFields must match WithField/WithGroupField output")
	assert.Equal(t, outSingle, outAttrs, "WithAttrs/WithGroupAttrs must match WithField/WithGroupField output")

	// Explicitly assert every expected key and nested group exists
	assert.Equal(t, "checkout-order-success", outSingle["message"])
	assert.Equal(t, "INFO", outSingle["level"])
	assert.Equal(t, "d4cda95b652f4a1592b449d5929fda1b", outSingle["traceId"])
	assert.Equal(t, "6e0c63257de34c92", outSingle["spanId"])
	assert.Equal(t, float64(2), outSingle["attempt"])
	assert.Equal(t, false, outSingle["cached"])

	actor := outSingle["actor"].(map[string]any)
	assert.Equal(t, "usr_123", actor["userId"])
	assert.Equal(t, "premium", actor["tier"])

	orderSvc := outSingle["orderService"].(map[string]any)
	checkout := orderSvc["checkout"].(map[string]any)
	assert.Equal(t, "ord_789", checkout["orderId"])
	assert.Equal(t, float64(4999), checkout["totalCents"])
	validate := orderSvc["validate"].(map[string]any)
	assert.Equal(t, true, validate["valid"])

	db := outSingle["db"].(map[string]any)
	orders := db["orders"].(map[string]any)
	insert := orders["insert"].(map[string]any)
	assert.Equal(t, "orders", insert["table"])
	assert.Equal(t, float64(4), insert["durationMs"])
}

// TestLoggingContract_AllValueTypesPreserved verifies every type switch branch in toSlogAttr
// so no supported type ever loses its key or value during flushing.
func TestLoggingContract_AllValueTypesPreserved(t *testing.T) {
	fixedTime := time.Date(2026, 10, 17, 4, 0, 0, 0, time.UTC)
	type customPayload struct {
		Name string `json:"name"`
	}

	ctx := NewContext(context.Background())
	WithField(ctx, "t_string", "hello")
	WithField(ctx, "t_int", int(-42))
	WithField(ctx, "t_int64", int64(9000000000))
	WithField(ctx, "t_int32", int32(-123))
	WithField(ctx, "t_uint", uint(42))
	WithField(ctx, "t_uint64", uint64(18000000000))
	WithField(ctx, "t_uint32", uint32(321))
	WithField(ctx, "t_float64", float64(3.14159))
	WithField(ctx, "t_float32", float32(2.5))
	WithField(ctx, "t_bool", true)
	WithField(ctx, "t_duration", 150*time.Millisecond)
	WithField(ctx, "t_time", fixedTime)
	WithField(ctx, "t_map", map[string]any{"nestedKey": "nestedVal"})
	WithField(ctx, "t_attr_keyless", slog.Attr{Key: "", Value: slog.StringValue("from_keyless_attr")})
	WithField(ctx, "t_attr_keyed", slog.String("explicit_key", "from_keyed_attr"))
	WithField(ctx, "t_slog_value", slog.IntValue(777))
	WithField(ctx, "t_log_valuer", sampleLogValuer{id: "ent_99"})
	WithField(ctx, "t_error", errors.New("boom error"))
	WithField(ctx, "t_custom_struct", customPayload{Name: "widget"})

	// Empty key in WithAttrs / WithGroupAttrs should be ignored without breaking valid keys
	WithAttrs(ctx, slog.Attr{Key: "", Value: slog.StringValue("ignored")}, slog.String("t_with_attr", "kept"))
	WithGroupAttrs(ctx, "grp", slog.Attr{Key: "", Value: slog.StringValue("ignored")}, slog.Int("g_count", 5))

	out := captureEvent(t, BagFromContext(ctx))

	assert.Equal(t, "hello", out["t_string"])
	assert.Equal(t, float64(-42), out["t_int"])
	assert.Equal(t, float64(9000000000), out["t_int64"])
	assert.Equal(t, float64(-123), out["t_int32"])
	assert.Equal(t, float64(42), out["t_uint"])
	assert.Equal(t, float64(18000000000), out["t_uint64"])
	assert.Equal(t, float64(321), out["t_uint32"])
	assert.InDelta(t, 3.14159, out["t_float64"], 1e-6)
	assert.InDelta(t, 2.5, out["t_float32"], 1e-6)
	assert.Equal(t, true, out["t_bool"])
	assert.Equal(t, float64(150*time.Millisecond), out["t_duration"])
	assert.Equal(t, fixedTime.Format(time.RFC3339Nano), out["t_time"])

	nestedMap, ok := out["t_map"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "nestedVal", nestedMap["nestedKey"])

	assert.Equal(t, "from_keyless_attr", out["t_attr_keyless"])
	assert.Equal(t, "from_keyed_attr", out["explicit_key"])
	assert.Equal(t, float64(777), out["t_slog_value"])

	valuerMap, ok := out["t_log_valuer"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "ent_99", valuerMap["entityId"])
	assert.Equal(t, true, valuerMap["redacted"])

	assert.Equal(t, "boom error", out["t_error"])

	customMap, ok := out["t_custom_struct"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "widget", customMap["name"])

	assert.Equal(t, "kept", out["t_with_attr"])
	grpMap, ok := out["grp"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, float64(5), grpMap["g_count"])
}

// TestLoggingContract_GroupPathEdgeCases verifies group path navigation, merging,
// overwriting non-map values with sub-groups, HasGroup, and getGroup consistency.
func TestLoggingContract_GroupPathEdgeCases(t *testing.T) {
	ctx := NewContext(context.Background())
	bag := BagFromContext(ctx)

	// 1. Multiple writes to same nested path merge keys without losing earlier ones
	WithGroupField(ctx, "a.b.c", "k1", "v1")
	WithGroupFields(ctx, "a.b.c", Fields{"k2": "v2"})
	WithGroupAttrs(ctx, "a.b.c", slog.String("k3", "v3"))

	// 2. Sibling groups at every level coexist
	WithGroupField(ctx, "a", "rootOfA", true)
	WithGroupField(ctx, "a.b", "rootOfB", true)
	WithGroupField(ctx, "a.b.sibling", "s1", 10)
	WithGroupField(ctx, "a.other", "o1", 20)

	// 3. Depth > 3 truncates to 3 levels without losing the field
	WithGroupField(ctx, "a.b.c.d.e", "truncatedKey", "preserved")

	assert.True(t, bag.HasGroup("a"))
	assert.True(t, bag.HasGroup("a.b"))
	assert.True(t, bag.HasGroup("a.b.c"))
	assert.True(t, bag.HasGroup("a.b.sibling"))
	assert.True(t, bag.HasGroup("a.other"))
	assert.False(t, bag.HasGroup("a.b.nonexistent"))
	assert.False(t, bag.HasGroup("a.nonexistent.child"))
	assert.False(t, bag.HasGroup("nonexistent"))

	out := captureEvent(t, bag)
	a := out["a"].(map[string]any)
	assert.Equal(t, true, a["rootOfA"])

	other := a["other"].(map[string]any)
	assert.Equal(t, float64(20), other["o1"])

	b := a["b"].(map[string]any)
	assert.Equal(t, true, b["rootOfB"])

	sibling := b["sibling"].(map[string]any)
	assert.Equal(t, float64(10), sibling["s1"])

	c := b["c"].(map[string]any)
	assert.Equal(t, "v1", c["k1"])
	assert.Equal(t, "v2", c["k2"])
	assert.Equal(t, "v3", c["k3"])
	assert.Equal(t, "preserved", c["truncatedKey"])
}

// TestLoggingContract_ExtractTraceEdgeCases verifies all valid and malformed variations
// of sentry-trace and baggage headers.
func TestLoggingContract_ExtractTraceEdgeCases(t *testing.T) {
	const valid32 = "d4cda95b652f4a1592b449d5929fda1b"

	tests := []struct {
		name        string
		sentryTrace string
		baggage     string
		wantTraceID string
		wantSpanID  string
		wantRandom  bool
	}{
		{
			name:        "valid 3-part sentry-trace",
			sentryTrace: valid32 + "-6e0c63257de34c92-1",
			wantTraceID: valid32,
			wantSpanID:  "6e0c63257de34c92",
		},
		{
			name:        "valid 2-part sentry-trace without sampled flag",
			sentryTrace: valid32 + "-6e0c63257de34c92",
			wantTraceID: valid32,
			wantSpanID:  "6e0c63257de34c92",
		},
		{
			name:        "sentry-trace with extra dashes in tail",
			sentryTrace: valid32 + "-6e0c63257de34c92-1-extra",
			wantTraceID: valid32,
			wantSpanID:  "6e0c63257de34c92",
		},
		{
			name:        "short trace ID falls back to baggage",
			sentryTrace: "short-6e0c63257de34c92-1",
			baggage:     "env=prod, sentry-trace_id=" + valid32 + " , sample=1",
			wantTraceID: valid32,
			wantSpanID:  "",
		},
		{
			name:        "sentry-trace without dash falls back to baggage",
			sentryTrace: valid32,
			baggage:     "sentry-trace_id=baggage-trace-1",
			wantTraceID: "baggage-trace-1",
			wantSpanID:  "",
		},
		{
			name:       "malformed baggage falls back to generated 32-char trace ID",
			baggage:    "invalid_entry,another=1,sentry-other=2",
			wantRandom: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			traceID, spanID := ExtractTrace(tt.sentryTrace, tt.baggage)
			if tt.wantRandom {
				assert.Len(t, traceID, 32)
				assert.Empty(t, spanID)
			} else {
				assert.Equal(t, tt.wantTraceID, traceID)
				assert.Equal(t, tt.wantSpanID, spanID)
			}
		})
	}
}

// TestLoggingContract_HTTPStackAllKeysPreserved verifies that a full HTTP request through
// HTTPMiddleware + HTTPTraceMiddleware preserves all 6 standard HTTP keys, trace keys,
// root keys, nested group keys, and error/result keys.
func TestLoggingContract_HTTPStackAllKeysPreserved(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(NewJSONHandler(&buf, nil))

	handler := HTTPHandler("checkout-order", func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()

		WithField(ctx, "tenantId", "tnt_42")
		WithAttrs(ctx, slog.String("region", "us-east-1"))
		WithGroupField(ctx, "actor", "userId", "usr_123")
		WithGroupFields(ctx, "request", Fields{"items": 3})
		WithGroupAttrs(ctx, "orderService.checkout",
			slog.String("orderId", "ord_789"),
			slog.Int64("totalCents", 4999),
		)

		err := fmt.Errorf("checkout failed: %w", &codedError{
			msg:      "card declined",
			code:     "CARD_DECLINED",
			category: "PAYMENT",
			fields:   map[string]any{" declineCode": "insufficient_funds"},
			cause:    errors.New("stripe: insufficient_funds"),
		})
		w.WriteHeader(http.StatusPaymentRequired)
		return err
	})

	srv := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(NewContextWithLogger(r.Context(), logger))
		HTTPMiddleware(HTTPTraceMiddleware(handler)).ServeHTTP(w, r)
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/checkout?currency=USD", nil)
	req.Header.Set(HeaderSentryTrace, "d4cda95b652f4a1592b449d5929fda1b-6e0c63257de34c92-1")
	req.Header.Set("X-Forwarded-For", " 198.51.100.7 , 10.0.0.1 ")
	req.Header.Set("User-Agent", "test-agent/2.0")
	w := httptest.NewRecorder()

	srv.ServeHTTP(w, req)

	var out map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &out))

	// Standard HTTP fields
	assert.Equal(t, "ERROR", out["level"])
	assert.Equal(t, "checkout-order-failure", out["message"])
	assert.Equal(t, float64(http.StatusPaymentRequired), out["status"])
	assert.Equal(t, "POST", out["method"])
	assert.Equal(t, "/v1/checkout?currency=USD", out["path"])
	assert.Equal(t, "198.51.100.7", out["ip"])
	assert.Equal(t, "test-agent/2.0", out["user-agent"])
	assert.NotEmpty(t, out["latency"])

	// Trace fields
	assert.Equal(t, "d4cda95b652f4a1592b449d5929fda1b", out["traceId"])
	assert.Equal(t, "6e0c63257de34c92", out["spanId"])

	// Root fields
	assert.Equal(t, "tnt_42", out["tenantId"])
	assert.Equal(t, "us-east-1", out["region"])

	// Nested groups
	actor := out["actor"].(map[string]any)
	assert.Equal(t, "usr_123", actor["userId"])

	requestGrp := out["request"].(map[string]any)
	assert.Equal(t, float64(3), requestGrp["items"])

	orderSvc := out["orderService"].(map[string]any)
	checkout := orderSvc["checkout"].(map[string]any)
	assert.Equal(t, "ord_789", checkout["orderId"])
	assert.Equal(t, float64(4999), checkout["totalCents"])

	// Error group
	errGrp := out["error"].(map[string]any)
	assert.Contains(t, errGrp["err"], "card declined")
	assert.Equal(t, "CARD_DECLINED", errGrp["code"])
	assert.Equal(t, "PAYMENT", errGrp["category"])
	assert.Equal(t, "stripe: insufficient_funds", errGrp["cause"])
}

// TestLoggingContract_ConsoleHandlerConcurrentIntegrity verifies that pooling buffers in
// ConsoleHandler never corrupts or mixes output across concurrent goroutines.
func TestLoggingContract_ConsoleHandlerConcurrentIntegrity(t *testing.T) {
	w := &syncWriter{}
	h := NewConsoleHandler(w, &ConsoleHandlerOptions{
		Level:   LevelTrace,
		NoColor: true,
	})
	logger := slog.New(h)

	const goroutines = 20
	const iterations = 25
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				ctx := NewContext(context.Background())
				WithField(ctx, "worker", id)
				WithGroupAttrs(ctx, "batch.run",
					slog.Int("iter", i),
					slog.String("tag", fmt.Sprintf("w%d_i%d", id, i)),
				)
				recordResult(ctx, "process-batch", nil)
				bag := BagFromContext(ctx)
				logger.LogAttrs(ctx, LevelInfo, bag.Message(), bag.Attrs()...)
			}
		}(g)
	}
	wg.Wait()

	lines := strings.Split(strings.TrimSpace(string(w.Bytes())), "\n")
	require.Len(t, lines, goroutines*iterations)

	for _, line := range lines {
		assert.Contains(t, line, "INF process-batch-success")
		assert.Contains(t, line, "worker=")
		assert.Contains(t, line, "batch={run={")
	}
}

// TestAllocations enforces deterministic zero-allocation budgets on hot paths.
func TestAllocations(t *testing.T) {
	bgCtx := context.Background()
	ctx, bag := newPopulatedBag()

	// Ensure warm-up and check if running under -race (which instruments every memory access)
	noopAllocs := testing.AllocsPerRun(100, func() {
		WithGroupField(bgCtx, "orderService.checkout", "orderId", "ord_789")
	})
	if noopAllocs > 0 {
		t.Fatalf("expected 0 allocations for no-op WithGroupField on empty context, got %v", noopAllocs)
	}

	t.Run("NoOp_ContextCalls_ZeroAllocs", func(t *testing.T) {
		allocs := testing.AllocsPerRun(100, func() {
			_ = BagFromContext(bgCtx)
			WithField(bgCtx, "k", "v")
			WithGroupField(bgCtx, "a.b.c", "k", "v")
			WithAttrs(bgCtx, slog.String("k", "v"))
			WithGroupAttrs(bgCtx, "a.b.c", slog.String("k", "v"))
			recordResult(bgCtx, "op", nil)
		})
		assert.Equal(t, float64(0), allocs, "no-op calls without FieldBag must not allocate")
	})

	t.Run("BagFromContext_Direct_ZeroAllocs", func(t *testing.T) {
		allocs := testing.AllocsPerRun(100, func() {
			_ = BagFromContext(ctx)
		})
		assert.Equal(t, float64(0), allocs, "direct BagFromContext lookup must not allocate")
	})

	t.Run("WithGroupField_ExistingPath_ZeroAllocs", func(t *testing.T) {
		allocs := testing.AllocsPerRun(100, func() {
			WithGroupField(ctx, "orderService.checkout", "orderId", "ord_789")
		})
		assert.Equal(t, float64(0), allocs, "WithGroupField on an existing dot-path must not allocate")
	})

	t.Run("HasGroup_DotPath_ZeroAllocs", func(t *testing.T) {
		allocs := testing.AllocsPerRun(100, func() {
			_ = bag.HasGroup("orderService.checkout")
			_ = bag.HasGroup("error")
		})
		assert.Equal(t, float64(0), allocs, "HasGroup must not allocate")
	})

	t.Run("HasWarning_ZeroAllocs", func(t *testing.T) {
		allocs := testing.AllocsPerRun(100, func() {
			_ = bag.HasWarning()
		})
		assert.Equal(t, float64(0), allocs, "HasWarning must not allocate")
	})

	t.Run("ExtractTrace_SentryTraceAndBaggage_ZeroAllocs", func(t *testing.T) {
		const st = "d4cda95b652f4a1592b449d5929fda1b-6e0c63257de34c92-1"
		const bg = "other=val,sentry-trace_id=d4cda95b652f4a1592b449d5929fda1b,env=prod"
		allocs := testing.AllocsPerRun(100, func() {
			_, _ = ExtractTrace(st, "")
			_, _ = ExtractTrace("", bg)
		})
		assert.Equal(t, float64(0), allocs, "ExtractTrace with sentry-trace or baggage header must not allocate")
	})

	t.Run("SamplingHandler_DroppedLog_ZeroAllocs", func(t *testing.T) {
		logger := slog.New(NewSamplingHandler(NewJSONHandler(io.Discard, nil), 0.0))
		allocs := testing.AllocsPerRun(100, func() {
			logger.LogAttrs(ctx, slog.LevelInfo, "dropped-event",
				slog.String("k", "v"),
			)
		})
		assert.Equal(t, float64(0), allocs, "dropped sampled log must not allocate")
	})

	t.Run("ConsoleHandler_ZeroAllocs", func(t *testing.T) {
		logger := slog.New(NewConsoleHandler(io.Discard, &ConsoleHandlerOptions{NoColor: false}))
		// <= 5 top-level attrs fits in slog.Record's inline array without stdlib allocating r.back
		inlineAttrs := bag.Attrs()[:5]
		msg := bag.Message()
		allocs := testing.AllocsPerRun(100, func() {
			logger.LogAttrs(ctx, slog.LevelInfo, msg, inlineAttrs...)
		})
		assert.Equal(t, float64(0), allocs, "ConsoleHandler emission must not allocate")
	})
}
