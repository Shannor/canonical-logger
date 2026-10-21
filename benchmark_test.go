package clog

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// benchLogValuer simulates a domain struct implementing slog.LogValuer for PII redaction.
type benchLogValuer struct {
	CustomerID string
	Email      string
	CartCents  int64
}

func (v benchLogValuer) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("customerId", v.CustomerID),
		slog.String("email", "[REDACTED]"),
		slog.Int64("cartCents", v.CartCents),
	)
}

// benchRichError simulates a domain error implementing Code(), Category(), ErrorFields(), and Unwrap().
type benchRichError struct {
	msg      string
	code     string
	category string
	fields   map[string]any
	cause    error
}

func (e *benchRichError) Error() string               { return e.msg }
func (e *benchRichError) Code() string                { return e.code }
func (e *benchRichError) Category() string            { return e.category }
func (e *benchRichError) ErrorFields() map[string]any { return e.fields }
func (e *benchRichError) Unwrap() error               { return e.cause }

func newPopulatedBag() (context.Context, *FieldBag) {
	ctx := NewContext(context.Background())
	WithField(ctx, "traceId", "d4cda95b652f4a1592b449d5929fda1b")
	WithField(ctx, "spanId", "6e0c63257de34c92")
	WithGroupAttrs(ctx, "actor",
		slog.String("userId", "usr_123"),
		slog.String("tier", "premium"),
	)
	WithGroupAttrs(ctx, "request",
		slog.String("idempotencyKey", "idem_abc"),
		slog.Int("items", 3),
	)
	WithGroupAttrs(ctx, "orderService.checkout",
		slog.String("orderId", "ord_789"),
		slog.Int64("totalCents", 4999),
	)
	WithGroupAttrs(ctx, "orderDAO.create",
		slog.String("table", "orders"),
		slog.Int64("durationMs", 4),
	)
	recordResult(ctx, "checkout-order", nil)
	return ctx, BagFromContext(ctx)
}

// ============================================================================
// A. Baseline ("Ergonomics Tax") Benchmark
// ============================================================================

func BenchmarkBaseline(b *testing.B) {
	logger := slog.New(NewJSONHandler(io.Discard, nil))
	bgCtx := context.Background()

	b.Run("RawSlog_LogAttrs", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			logger.LogAttrs(bgCtx, slog.LevelInfo, "checkout-order-success",
				slog.Int("status", 200),
				slog.String("method", "POST"),
				slog.String("path", "/v1/orders/checkout"),
				slog.String("ip", "203.0.113.195"),
				slog.String("latency", "4.12ms"),
				slog.String("user-agent", "checkout-client/1.0"),
				slog.String("traceId", "d4cda95b652f4a1592b449d5929fda1b"),
				slog.String("spanId", "6e0c63257de34c92"),
				slog.GroupAttrs("actor",
					slog.String("userId", "usr_123"),
					slog.String("tier", "premium"),
				),
				slog.GroupAttrs("request",
					slog.String("idempotencyKey", "idem_abc"),
					slog.Int("items", 3),
				),
				slog.GroupAttrs("orderService",
					slog.GroupAttrs("checkout",
						slog.String("orderId", "ord_789"),
						slog.Int64("totalCents", 4999),
					),
				),
				slog.GroupAttrs("orderDAO",
					slog.GroupAttrs("create",
						slog.String("table", "orders"),
						slog.Int64("durationMs", 4),
					),
				),
			)
		}
	})

	b.Run("Clog_WithGroupAttrs", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			ctx := NewContext(bgCtx)
			WithAttrs(ctx,
				slog.String("traceId", "d4cda95b652f4a1592b449d5929fda1b"),
				slog.String("spanId", "6e0c63257de34c92"),
			)
			WithGroupAttrs(ctx, "actor",
				slog.String("userId", "usr_123"),
				slog.String("tier", "premium"),
			)
			WithGroupAttrs(ctx, "request",
				slog.String("idempotencyKey", "idem_abc"),
				slog.Int("items", 3),
			)
			WithGroupAttrs(ctx, "orderService.checkout",
				slog.String("orderId", "ord_789"),
				slog.Int64("totalCents", 4999),
			)
			WithGroupAttrs(ctx, "orderDAO.create",
				slog.String("table", "orders"),
				slog.Int64("durationMs", 4),
			)
			recordResult(ctx, "checkout-order", nil)

			bag := BagFromContext(ctx)
			attrs := []slog.Attr{
				slog.Int("status", 200),
				slog.String("method", "POST"),
				slog.String("path", "/v1/orders/checkout"),
				slog.String("ip", "203.0.113.195"),
				slog.String("latency", "4.12ms"),
				slog.String("user-agent", "checkout-client/1.0"),
			}
			attrs = append(attrs, bag.Attrs()...)
			logger.LogAttrs(ctx, slog.LevelInfo, bag.Message(), attrs...)
		}
	})

	b.Run("Clog_WithGroupField", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			ctx := NewContext(bgCtx)
			WithField(ctx, "traceId", "d4cda95b652f4a1592b449d5929fda1b")
			WithField(ctx, "spanId", "6e0c63257de34c92")
			WithGroupField(ctx, "actor", "userId", "usr_123")
			WithGroupField(ctx, "actor", "tier", "premium")
			WithGroupField(ctx, "request", "idempotencyKey", "idem_abc")
			WithGroupField(ctx, "request", "items", 3)
			WithGroupField(ctx, "orderService.checkout", "orderId", "ord_789")
			WithGroupField(ctx, "orderService.checkout", "totalCents", int64(4999))
			WithGroupField(ctx, "orderDAO.create", "table", "orders")
			WithGroupField(ctx, "orderDAO.create", "durationMs", int64(4))
			recordResult(ctx, "checkout-order", nil)

			bag := BagFromContext(ctx)
			attrs := []slog.Attr{
				slog.Int("status", 200),
				slog.String("method", "POST"),
				slog.String("path", "/v1/orders/checkout"),
				slog.String("ip", "203.0.113.195"),
				slog.String("latency", "4.12ms"),
				slog.String("user-agent", "checkout-client/1.0"),
			}
			attrs = append(attrs, bag.Attrs()...)
			logger.LogAttrs(ctx, slog.LevelInfo, bag.Message(), attrs...)
		}
	})

	b.Run("Clog_WithGroupFields_MapLiteral", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			ctx := NewContext(bgCtx)
			WithFields(ctx, Fields{
				"traceId": "d4cda95b652f4a1592b449d5929fda1b",
				"spanId":  "6e0c63257de34c92",
			})
			WithGroupFields(ctx, "actor", Fields{
				"userId": "usr_123",
				"tier":   "premium",
			})
			WithGroupFields(ctx, "request", Fields{
				"idempotencyKey": "idem_abc",
				"items":          3,
			})
			WithGroupFields(ctx, "orderService.checkout", Fields{
				"orderId":    "ord_789",
				"totalCents": int64(4999),
			})
			WithGroupFields(ctx, "orderDAO.create", Fields{
				"table":      "orders",
				"durationMs": int64(4),
			})
			recordResult(ctx, "checkout-order", nil)

			bag := BagFromContext(ctx)
			attrs := []slog.Attr{
				slog.Int("status", 200),
				slog.String("method", "POST"),
				slog.String("path", "/v1/orders/checkout"),
				slog.String("ip", "203.0.113.195"),
				slog.String("latency", "4.12ms"),
				slog.String("user-agent", "checkout-client/1.0"),
			}
			attrs = append(attrs, bag.Attrs()...)
			logger.LogAttrs(ctx, slog.LevelInfo, bag.Message(), attrs...)
		}
	})
}

// ============================================================================
// B. Accumulation API Comparison & Group Path Depth
// ============================================================================

func BenchmarkAccumulation(b *testing.B) {
	b.Run("Root_WithField", func(b *testing.B) {
		ctx := NewContext(context.Background())
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			WithField(ctx, "region", "us-east-1")
			WithField(ctx, "attempt", 2)
		}
	})

	b.Run("Root_WithFields_MapLiteral", func(b *testing.B) {
		ctx := NewContext(context.Background())
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			WithFields(ctx, Fields{
				"region":  "us-east-1",
				"attempt": 2,
			})
		}
	})

	b.Run("Root_WithAttrs", func(b *testing.B) {
		ctx := NewContext(context.Background())
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			WithAttrs(ctx,
				slog.String("region", "us-east-1"),
				slog.Int("attempt", 2),
			)
		}
	})

	b.Run("Group_WithGroupField", func(b *testing.B) {
		ctx := NewContext(context.Background())
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			WithGroupField(ctx, "orderService.checkout", "orderId", "ord_789")
			WithGroupField(ctx, "orderService.checkout", "totalCents", int64(4999))
		}
	})

	b.Run("Group_WithGroupFields_MapLiteral", func(b *testing.B) {
		ctx := NewContext(context.Background())
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			WithGroupFields(ctx, "orderService.checkout", Fields{
				"orderId":    "ord_789",
				"totalCents": int64(4999),
			})
		}
	})

	b.Run("Group_WithGroupAttrs", func(b *testing.B) {
		ctx := NewContext(context.Background())
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			WithGroupAttrs(ctx, "orderService.checkout",
				slog.String("orderId", "ord_789"),
				slog.Int64("totalCents", 4999),
			)
		}
	})

	b.Run("Group_WithGroupField_LogValuer", func(b *testing.B) {
		ctx := NewContext(context.Background())
		valuer := benchLogValuer{
			CustomerID: "cust_123",
			Email:      "alice@example.com",
			CartCents:  4999,
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			WithGroupField(ctx, "orderService.checkout", "input", valuer)
		}
	})

	b.Run("NoOp_WithGroupField", func(b *testing.B) {
		bgCtx := context.Background()
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			WithGroupField(bgCtx, "orderService.checkout", "orderId", "ord_789")
		}
	})

	b.Run("NoOp_WithGroupAttrs", func(b *testing.B) {
		bgCtx := context.Background()
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			WithGroupAttrs(bgCtx, "orderService.checkout",
				slog.String("orderId", "ord_789"),
				slog.Int64("totalCents", 4999),
			)
		}
	})
}

func BenchmarkGroupPathDepth(b *testing.B) {
	b.Run("Depth1_Flat", func(b *testing.B) {
		ctx := NewContext(context.Background())
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			WithGroupField(ctx, "request", "items", 3)
		}
	})

	b.Run("Depth2_Nested", func(b *testing.B) {
		ctx := NewContext(context.Background())
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			WithGroupField(ctx, "orderService.checkout", "items", 3)
		}
	})

	b.Run("Depth3_Deep", func(b *testing.B) {
		ctx := NewContext(context.Background())
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			WithGroupField(ctx, "db.orders.insert", "items", 3)
		}
	})
}

// ============================================================================
// C. Benchmarking the 4 Exported Ports
// ============================================================================

func BenchmarkPort1_ContextResolution(b *testing.B) {
	// Save and restore global contextResolvers around this benchmark.
	contextResolversMu.Lock()
	savedResolvers := contextResolvers
	contextResolvers = nil
	contextResolversMu.Unlock()
	defer func() {
		contextResolversMu.Lock()
		contextResolvers = savedResolvers
		contextResolversMu.Unlock()
	}()

	b.Run("Direct_FastPath", func(b *testing.B) {
		ctx := NewContext(context.Background())
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = BagFromContext(ctx)
		}
	})

	b.Run("NoOp_EmptyContext_NoResolvers", func(b *testing.B) {
		ctx := context.Background()
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = BagFromContext(ctx)
		}
	})

	type customAdapterKey struct{}
	RegisterContextResolver(func(ctx context.Context) *FieldBag {
		if bag, ok := ctx.Value(customAdapterKey{}).(*FieldBag); ok {
			return bag
		}
		return nil
	})

	b.Run("Fallback_ContextResolver", func(b *testing.B) {
		bag := &FieldBag{}
		ctx := context.WithValue(context.Background(), customAdapterKey{}, bag)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = BagFromContext(ctx)
		}
	})

	b.Run("NoOp_EmptyContext_WithResolver", func(b *testing.B) {
		ctx := context.Background()
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = BagFromContext(ctx)
		}
	})
}

func BenchmarkPort2_BoundaryInspection(b *testing.B) {
	_, bag := newPopulatedBag()

	b.Run("HasGroup_Depth1", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = bag.HasGroup("request")
		}
	})

	b.Run("HasGroup_Depth2", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = bag.HasGroup("orderService.checkout")
		}
	})

	b.Run("HasGroup_Missing", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = bag.HasGroup("error")
		}
	})

	b.Run("HasWarning_False", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = bag.HasWarning()
		}
	})

	b.Run("HasWarning_True_Nested", func(b *testing.B) {
		_, warnBag := newPopulatedBag()
		warnBag.setGroup("orderDAO.create", "warning", "slow query")
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = warnBag.HasWarning()
		}
	})

	b.Run("Message", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = bag.Message()
		}
	})

	b.Run("Attrs", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = bag.Attrs()
		}
	})
}

func BenchmarkPort3_ExtractTrace(b *testing.B) {
	const validSentryTrace = "d4cda95b652f4a1592b449d5929fda1b-6e0c63257de34c92-1"
	const validBaggage = "other=val,sentry-trace_id=d4cda95b652f4a1592b449d5929fda1b,env=prod"

	b.Run("SentryTrace_Valid", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _ = ExtractTrace(validSentryTrace, "")
		}
	})

	b.Run("Baggage_Fallback", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _ = ExtractTrace("", validBaggage)
		}
	})

	b.Run("GenerateTraceID_Fallback", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _ = ExtractTrace("", "")
		}
	})
}

func BenchmarkPort4_ResultFormatting(b *testing.B) {
	rootCause := errors.New("pq: duplicate key value violates unique constraint")
	richErr := fmt.Errorf("create order failed: %w", &benchRichError{
		msg:      "order conflict",
		code:     "ORDER_ALREADY_EXISTS",
		category: "CONFLICT",
		fields: map[string]any{
			"orderId": "ord_789",
			"retry":   false,
		},
		cause: rootCause,
	})

	b.Run("RecordResult_Success", func(b *testing.B) {
		ctx := NewContext(context.Background())
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			recordResult(ctx, "checkout-order", nil)
		}
	})

	b.Run("RecordResult_Failure_RichError", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			ctx := NewContext(context.Background())
			recordResult(ctx, "checkout-order", richErr)
		}
	})

	b.Run("Run_Success", func(b *testing.B) {
		ctx := NewContext(context.Background())
		fn := func(context.Context) error { return nil }
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = Run(ctx, "checkout-order", fn)
		}
	})

	b.Run("Run_Failure_RichError", func(b *testing.B) {
		fn := func(context.Context) error { return richErr }
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			ctx := NewContext(context.Background())
			_ = Run(ctx, "checkout-order", fn)
		}
	})
}

// ============================================================================
// D. End-to-End Middleware, Handlers & Background Tasks
// ============================================================================

type discardResponseWriter struct {
	header http.Header
	code   int
}

func (d *discardResponseWriter) Header() http.Header         { return d.header }
func (d *discardResponseWriter) Write(p []byte) (int, error) { return len(p), nil }
func (d *discardResponseWriter) WriteHeader(statusCode int)  { d.code = statusCode }

func BenchmarkHTTPStack(b *testing.B) {
	logger := slog.New(NewJSONHandler(io.Discard, nil))

	appHandler := HTTPHandler("checkout-order", func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()

		WithGroupAttrs(ctx, "actor",
			slog.String("userId", "usr_123"),
			slog.String("tier", "premium"),
		)
		WithGroupAttrs(ctx, "request",
			slog.String("idempotencyKey", "idem_abc"),
			slog.Int("items", 3),
		)
		WithGroupAttrs(ctx, "orderService.checkout",
			slog.String("orderId", "ord_789"),
			slog.Int64("totalCents", 4999),
		)
		WithGroupAttrs(ctx, "orderDAO.create",
			slog.String("table", "orders"),
			slog.Int64("durationMs", 4),
		)
		w.WriteHeader(http.StatusOK)
		return nil
	})

	stack := HTTPMiddleware(HTTPTraceMiddleware(appHandler))

	baseReq := httptest.NewRequest(http.MethodPost, "/v1/orders/checkout?source=mobile", nil)
	baseReq.Header.Set(HeaderSentryTrace, "d4cda95b652f4a1592b449d5929fda1b-6e0c63257de34c92-1")
	baseReq.Header.Set("X-Forwarded-For", "203.0.113.195, 70.41.3.18")
	baseReq.Header.Set("User-Agent", "checkout-client/1.0")
	baseReq = baseReq.WithContext(NewContextWithLogger(baseReq.Context(), logger))

	rw := &discardResponseWriter{header: make(http.Header, 2)}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		stack.ServeHTTP(rw, baseReq)
	}
}

func BenchmarkHandlers(b *testing.B) {
	ctx, bag := newPopulatedBag()
	attrs := bag.Attrs()
	msg := bag.Message()

	b.Run("JSONHandler_Default", func(b *testing.B) {
		logger := slog.New(NewJSONHandler(io.Discard, nil))
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			logger.LogAttrs(ctx, slog.LevelInfo, msg, attrs...)
		}
	})

	b.Run("JSONHandler_GCPReplaceAttr", func(b *testing.B) {
		logger := slog.New(NewJSONHandler(io.Discard, &slog.HandlerOptions{
			ReplaceAttr: GCPReplaceAttr,
		}))
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			logger.LogAttrs(ctx, slog.LevelInfo, msg, attrs...)
		}
	})

	b.Run("ConsoleHandler_Color", func(b *testing.B) {
		logger := slog.New(NewConsoleHandler(io.Discard, &ConsoleHandlerOptions{NoColor: false}))
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			logger.LogAttrs(ctx, slog.LevelInfo, msg, attrs...)
		}
	})

	b.Run("ConsoleHandler_NoColor", func(b *testing.B) {
		logger := slog.New(NewConsoleHandler(io.Discard, &ConsoleHandlerOptions{NoColor: true}))
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			logger.LogAttrs(ctx, slog.LevelInfo, msg, attrs...)
		}
	})

	b.Run("SamplingHandler_0Pct_Dropped", func(b *testing.B) {
		logger := slog.New(NewSamplingHandler(NewJSONHandler(io.Discard, nil), 0.0))
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			logger.LogAttrs(ctx, slog.LevelInfo, msg, attrs...)
		}
	})

	b.Run("SamplingHandler_10Pct", func(b *testing.B) {
		logger := slog.New(NewSamplingHandler(NewJSONHandler(io.Discard, nil), 0.1))
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			logger.LogAttrs(ctx, slog.LevelInfo, msg, attrs...)
		}
	})

	b.Run("SamplingHandler_100Pct", func(b *testing.B) {
		logger := slog.New(NewSamplingHandler(NewJSONHandler(io.Discard, nil), 1.0))
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			logger.LogAttrs(ctx, slog.LevelInfo, msg, attrs...)
		}
	})

	b.Run("SamplingHandler_WithEvaluator", func(b *testing.B) {
		logger := slog.New(NewSamplingHandlerWithEvaluator(
			NewJSONHandler(io.Discard, nil),
			0.0,
			func(_ context.Context, bag *FieldBag) bool {
				return bag != nil && bag.HasGroup("orderService.checkout")
			},
		))
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			logger.LogAttrs(ctx, slog.LevelInfo, msg, attrs...)
		}
	})
}

func BenchmarkBackground(b *testing.B) {
	prevDefault := slog.Default()
	slog.SetDefault(slog.New(NewJSONHandler(io.Discard, nil)))
	defer slog.SetDefault(prevDefault)

	b.Run("Snapshot_DeepCopy", func(b *testing.B) {
		_, bag := newPopulatedBag()
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = bag.snapshot()
		}
	})

	b.Run("Func", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			Func("sync-inventory", func(ctx context.Context) error {
				WithGroupAttrs(ctx, "inventoryService.sync",
					slog.Int("syncedSkus", 42),
					slog.Int64("durationMs", 12),
				)
				return nil
			})
		}
	})

	b.Run("Go_WithParentSnapshot", func(b *testing.B) {
		parentCtx, _ := newPopulatedBag()
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			var wg sync.WaitGroup
			wg.Add(1)
			Go(parentCtx, "send-receipt-email", func(ctx context.Context) error {
				defer wg.Done()
				WithGroupField(ctx, "mailer", "template", "order-receipt")
				return nil
			})
			wg.Wait()
		}
	})
}
