# canonical-logger (`clog`)

[![Go Reference](https://pkg.go.dev/badge/github.com/Shannor/canonical-logger.svg)](https://pkg.go.dev/github.com/Shannor/canonical-logger)
[![Go Report Card](https://goreportcard.com/badge/github.com/Shannor/canonical-logger)](https://goreportcard.com/report/github.com/Shannor/canonical-logger)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

Structured canonical logging for Go, built on Go's standard library **`log/slog`**.

`canonical-logger` (`clog`) implements the **Canonical Log Line** pattern (pioneered by Stripe): emitting exactly **one rich, structured log line** per HTTP request or background task lifecycle. Rather than scattering noisy, disjointed log lines across handlers, services, and database layers, contextual metadata is accumulated in a thread-safe `FieldBag` across the execution lifecycle and emitted as a single structured JSON event.

For AI agents and automated coding workflows, see [AGENTS.md](AGENTS.md) for strict architectural rules, layer boundaries, and linting checklists.

---

## Highlights

- **Standard Library First**: Built directly on `log/slog` (Go 1.21+) with **zero third-party logging dependencies**.
- **Single Structured Event**: One log entry per request with complete operational, business, and error context.
- **Hierarchical Groups**: Organize context cleanly using nested JSON objects with dot-separated paths (e.g. `clog.WithGroupField(ctx, "db.getCampaign", "rows", 12)`).
- **HTTP Middlewares**: First-class support for both [Gin](https://github.com/gin-gonic/gin) (`GinMiddleware`) and standard `net/http` (`HTTPMiddleware`, compatible with Chi, Gorilla, etc.).
- **Trace Propagation**: Out-of-the-box W3C / Sentry trace ID extraction (`sentry-trace`, `baggage`) and fallback generation with response header injection.
- **Boundary Error Tracking**: `SetResult` and `DeferResult` automatically record error details and format message tags (`<event>-success`, `<event>-failure`).
- **Async & Background Support**:
  - `clog.Go`: Spawns detached goroutines with panic recovery, independent log sessions, and parent fieldbag correlation.
  - `clog.Func`: Synchronous tasks with automatic latency and line emission.
  - `clog.CronFunc`: Wraps scheduled jobs for cron runners.
- **Adaptive Level Sampling**: `NewSamplingHandler` guarantees 100% capture of Warnings, Errors, and Panics while sampling high-volume Info/Debug traffic.
- **Cloud Logging Ready**: Includes `GCPReplaceAttr` for automatic GCP severity mapping and `GCPWriter` for direct Google Cloud Logging SDK streaming.

---

## Installation

```bash
go get github.com/Shannor/canonical-logger
```

You can import either the root package:

```go
import "github.com/Shannor/canonical-logger"
```

or the `pkg/clog` subpackage:

```go
import "github.com/Shannor/canonical-logger/pkg/clog"
```

Both export identical APIs under the `clog` package identifier. *(For backward compatibility with existing projects, `pkg/xlog` is also provided as an alias).*

---

## Architectural Principles & Layer Responsibilities

Canonical logging relies on a clear separation of concerns across application layers. Each layer has specific responsibilities for what it enriches:

| Layer | Functions Permitted | Group Name | Purpose & Invariants |
| --- | --- | --- | --- |
| **Middleware** | `GinMiddleware`, `HTTPMiddleware`, `TraceID`, Auth middlewares | `actor` / root | Emits HTTP root fields (`status`, `method`, `path`, `ip`, `latency`, `user-agent`, `traceId`, `spanId`) and identity context (`actor`). |
| **Boundary (API / Webhooks)** | `SetResult()`, `DeferResult()`, `WithGroupFields()` | `request` | Owns the canonical event name (`-success` / `-failure`), adds request input parameters & query filters (`request`). |
| **Service Layer** | `WithGroupFields()`, `WithGroupField()` | `<serviceName>.<methodName>` | Records business execution details. **MUST NOT** set event names or touch the `"error"` group. Returns standard Go errors. |
| **DAO / DB Layer** | `WithGroupFields()`, `WithGroupField()` | `<daoName>.<methodName>` or `db.<methodName>` | Records query metadata (`table`, `rowsReturned`, `durationMs`). **MUST NOT** set event names or touch `"error"`. Returns standard Go errors. |
| **Background / Cron** | `clog.Go()`, `clog.CronFunc()`, `clog.Func()` | `job` / `request` | Spawns isolated execution contexts with independent FieldBags, correlates with parent snapshots, and recovers from panics. |

---

## Field & Group Naming Conventions

Structured log engines (such as GCP Cloud Logging, Datadog, and Elasticsearch) query nested JSON via path expressions like `jsonPayload.<group>.<field>`. Consistent naming is critical.

### Group Naming Rules

- **Middleware**: Group MUST be `actor` (e.g. `requestorId`, `isAdmin`, `targetId`).
- **API Handlers**: Group MUST be `request` (e.g. `campaignId`, `limit`, `offset`, `filters`).
- **Service Layer**: Group MUST follow `<serviceName>.<methodName>` (e.g. `campaignService.create`, `paymentService.charge`).
- **DAO / DB Layer**: Group MUST follow `<daoName>.<methodName>` or `db.<methodName>` (e.g. `campaignDAO.getByID`, `db.queryAttendees`).
- **Error Group**: Group MUST be `error`. Populated **automatically** by `SetResult` / `DeferResult` on failure.

### Field Naming Rules

1. **camelCase Map Keys**: All keys in `clog.Fields` MUST use **camelCase** (e.g. `"userId"`, `"campaignId"`, `"rowsReturned"`, `"durationMs"`).
2. **The `Id` Suffix**: Always use `Id` (not `ID`) in JSON string keys so search queries across entities are uniform (`"userId": userID`, `"campaignId": campaignID`). Go variable names remain standard Go idiom (`userID`, `campaignID`).
3. **Always Use `clog.Fields`**: Use the package type alias `clog.Fields{...}` when constructing field maps. Never use bare `map[string]any` or `map[any]any`.
4. **Zero PII Policy**: **Never log PII** (phone numbers, email addresses, full names, physical addresses). Always log entity identifiers (UUIDs, DB IDs).

---

## Example Canonical Log Line

Below is an actual canonical log line emitted after a request passes through middleware, API handler, service layer, and database DAO:

```json
{
  "time": "2026-10-11T22:30:00.000000000-04:00",
  "level": "INFO",
  "message": "checkout-order-success",
  "status": 200,
  "method": "POST",
  "path": "/api/v1/checkout",
  "latency": "142ms",
  "ip": "192.168.1.10",
  "user-agent": "MyApp-iOS/2.1",
  "traceId": "827fddb6f17a434f9118dc7bca3e0f22",
  "spanId": "9c6e42da7199350c",
  "actor": {
    "requestorId": "usr_94103",
    "isAdmin": false
  },
  "request": {
    "cartId": "cart_xyz789",
    "currency": "USD",
    "itemCount": 3
  },
  "orderService.checkout": {
    "orderId": "ord_1001",
    "totalCents": 4500,
    "discountApplied": true
  },
  "paymentService.charge": {
    "chargeId": "ch_3Mtw",
    "provider": "stripe",
    "status": "succeeded"
  },
  "orderDAO.create": {
    "table": "orders",
    "rows": 1,
    "durationMs": 4
  }
}
```

---

## End-to-End Multi-Layer Example

Here is how context accumulates cleanly across the application layers without passing logger instances:

### 1. HTTP Handler (Boundary Layer)

The handler uses `defer clog.DeferResult` to capture the final outcome and tags the `request` group with input parameters:

```go
func (api OrderAPI) Checkout(c *gin.Context) {
    ctx := c.Request.Context()
    var err error
    defer clog.DeferResult(ctx, "checkout-order", &err)

    var req CheckoutRequest
    if err = c.ShouldBindJSON(&req); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
        return
    }

    // Enrich request group
    clog.WithGroupFields(ctx, "request", clog.Fields{
        "cartId":    req.CartID,
        "currency":  req.Currency,
        "itemCount": len(req.Items),
    })

    var order *models.Order
    order, err = api.orderService.Checkout(ctx, req)
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
        return
    }

    c.JSON(http.StatusOK, order)
}
```

### 2. Service Layer (Business Execution)

The service method performs business logic, enriches its group `<serviceName>.<methodName>`, and returns standard errors:

```go
func (s orderService) Checkout(ctx context.Context, req CheckoutRequest) (*models.Order, error) {
    order, err := s.dao.CreateOrder(ctx, req)
    if err != nil {
        // Wrap and return error standardly; DO NOT populate the "error" group
        return nil, fmt.Errorf("orderService.Checkout: %w", err)
    }

    // Enrich service group
    clog.WithGroupFields(ctx, "orderService.checkout", clog.Fields{
        "orderId":         order.ID,
        "totalCents":      order.TotalCents,
        "discountApplied": req.PromoCode != "",
    })

    return order, nil
}
```

### 3. DAO Layer (Database / Storage Execution)

The DAO method queries the database, records execution metrics in `<daoName>.<methodName>`, and returns standard errors:

```go
func (d orderDAO) CreateOrder(ctx context.Context, req CheckoutRequest) (*models.Order, error) {
    start := time.Now()
    order := &models.Order{
        ID:         uuid.NewString(),
        CartID:     req.CartID,
        TotalCents: req.TotalCents,
    }

    err := d.db.WithContext(ctx).Create(order).Error

    // Enrich DAO group
    clog.WithGroupFields(ctx, "orderDAO.create", clog.Fields{
        "table":      "orders",
        "rows":       1,
        "durationMs": time.Since(start).Milliseconds(),
    })

    if err != nil {
        return nil, fmt.Errorf("orderDAO.CreateOrder: %w", err)
    }
    return order, nil
}
```

---

## Result Tracking & The Error Shadowing Gotcha

### How `SetResult` and `DeferResult` Work

Boundary layers call `SetResult` or `DeferResult` with the base event name:
- On success (`err == nil`): The message is set to `"<event>-success"`.
- On failure (`err != nil`): The message is set to `"<event>-failure"`, the canonical log escalates to `ERROR` level, and the `"error"` group is populated with `{"err": err.Error()}`.

### ⚠️ Warning: The Error Shadowing Gotcha

When using `defer clog.DeferResult(ctx, "my-operation", &err)`, the deferred call evaluates the pointer `&err` at function return. **If you shadow `err` inside an `if`, `for`, or `switch` block with `:=`, the outer `err` remains `nil`, causing the log to falsely report success!**

```go
// ❌ WRONG: := in a nested block shadows the outer 'err'
func (api API) Handle(c *gin.Context) {
    ctx := c.Request.Context()
    var err error
    defer clog.DeferResult(ctx, "handle-action", &err)

    if condition {
        // := creates a new local 'err' scoped only to this if block!
        data, err := api.service.Process(ctx)
        if err != nil {
            c.JSON(500, gin.H{"error": err.Error()})
            return // BUG: outer 'err' is still nil -> logs "handle-action-success"!
        }
    }
}

// ✅ CORRECT: Assign to the outer 'err' using '='
func (api API) Handle(c *gin.Context) {
    ctx := c.Request.Context()
    var err error
    defer clog.DeferResult(ctx, "handle-action", &err)

    if condition {
        var data *Data
        data, err = api.service.Process(ctx) // Reuses the outer 'err'
        if err != nil {
            c.JSON(500, gin.H{"error": err.Error()})
            return // Correctly logs "handle-action-failure" with error group!
        }
    }
}
```

---

## Quick Start

### 1. With Gin

```go
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"

	"github.com/Shannor/canonical-logger"
	"github.com/gin-gonic/gin"
)

func main() {
	// Configure slog JSON handler with standard "message" key
	logger := slog.New(clog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	r := gin.New()

	// Register canonical logger and trace middleware
	r.Use(clog.GinMiddleware())
	r.Use(clog.TraceID())

	r.POST("/api/v1/campaigns", func(c *gin.Context) {
		ctx := c.Request.Context()

		var err error
		defer clog.DeferResult(ctx, "create-campaign", &err)

		// Enrich request context
		clog.WithGroupFields(ctx, "request", clog.Fields{
			"name":   "Summer Launch",
			"budget": 5000,
		})

		// Call business layer passing ctx
		err = createCampaignService(ctx, "Summer Launch")
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	r.Run(":8080")
}

func createCampaignService(ctx context.Context, name string) error {
	clog.WithGroupField(ctx, "campaignService.create", "campaignId", "cmp-456")
	clog.WithGroupFields(ctx, "db.insertCampaign", clog.Fields{
		"table":      "campaigns",
		"durationMs": 12,
	})
	return nil
}
```

### 2. With Standard `net/http` / Chi

```go
package main

import (
	"log/slog"
	"net/http"
	"os"

	"github.com/Shannor/canonical-logger"
)

func main() {
	slog.SetDefault(slog.New(clog.NewJSONHandler(os.Stdout, nil)))

	mux := http.NewServeMux()

	mux.HandleFunc("GET /users/{id}", func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		clog.WithGroupField(ctx, "user", "id", r.PathValue("id"))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})

	// Wrap handler with clog HTTPMiddleware and HTTPTraceMiddleware
	handler := clog.HTTPMiddleware(clog.HTTPTraceMiddleware(mux))
	http.ListenAndServe(":8080", handler)
}
```

---

## Context API Reference

All `clog` context helpers are safe to call when no `FieldBag` is in context (they become safe no-ops).

### Adding Fields

```go
// Add a flat root-level field
clog.WithField(ctx, "requestId", "req-123")

// Add multiple root-level fields
clog.WithFields(ctx, clog.Fields{
    "version": "1.0.0",
    "region":  "us-east-1",
})

// Add a single field to a nested group
clog.WithGroupField(ctx, "actor", "userId", "usr-abc")

// Add multiple fields to a group (supports dot-separated paths up to 3 levels deep)
clog.WithGroupFields(ctx, "db.getUser", clog.Fields{
    "queryDurationMs": 14,
    "rows":            1,
})

// Override the default canonical message ("canonical-log-line")
clog.WithMessage(ctx, "custom-event-message")
```

---

## Background Tasks & Goroutines

### `clog.Go`: Detached Background Goroutine

Spawns a goroutine decoupled from the parent context's cancellation, with:
- Its own fresh `FieldBag`.
- A snapshot of the parent's `FieldBag` attached as a `"parent"` group for log correlation.
- Panic recovery logging with automatic stack error capture and canonical flush.

```go
clog.Go(ctx, "send-welcome-email", func(bgCtx context.Context) {
    var err error
    defer clog.DeferResult(bgCtx, "send-welcome-email", &err)

    err = emailService.Send(bgCtx, userEmail)
})
```

### `clog.Func`: Synchronous Task

Runs a synchronous block with its own canonical logging context:

```go
clog.Func("startup-migrations", func(ctx context.Context) {
    var err error
    defer clog.DeferResult(ctx, "startup-migrations", &err)

    err = runMigrations(ctx)
})
```

### `clog.CronFunc`: Scheduled Job

Wraps a function for use with cron job schedulers (such as `robfig/cron`):

```go
c := cron.New()
c.AddFunc("@hourly", clog.CronFunc("sync-subscribers", func(ctx context.Context) {
    var err error
    defer clog.DeferResult(ctx, "sync-subscribers", &err)

    err = syncService.Sync(ctx)
}))
```

---

## Level Sampling

Use `NewSamplingHandler` to wrap any `slog.Handler` to sample high-frequency info/debug logs while always keeping 100% of warnings and errors:

```go
// Sample 10% of Info/Debug logs, but keep 100% of Warn/Error
baseHandler := clog.NewJSONHandler(os.Stdout, nil)
samplingHandler := clog.NewSamplingHandler(baseHandler, 0.10)

slog.SetDefault(slog.New(samplingHandler))
```

---

## Google Cloud Logging Integration

### Option A: Standard JSON to Stdout (Cloud Run, GKE, Compute Engine)

GCP Cloud Logging automatically parses stdout JSON. Use `clog.GCPReplaceAttr` to map the level key to `"severity"` (with values `"DEBUG"`, `"INFO"`, `"WARNING"`, `"ERROR"`) and message to `"message"`:

```go
import (
    "log/slog"
    "os"

    "github.com/Shannor/canonical-logger"
)

handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
    ReplaceAttr: clog.GCPReplaceAttr,
})
slog.SetDefault(slog.New(handler))
```

### Option B: Google Cloud Logging SDK (`GCPWriter`)

If using the GCP Cloud Logging Go SDK directly:

```go
import (
    "log/slog"
    "cloud.google.com/go/logging"
    "github.com/Shannor/canonical-logger"
)

client, err := logging.NewClient(ctx, "my-gcp-project-id")
if err != nil {
    panic(err)
}
defer client.Close()

gcpLogger := client.Logger("my-service-name")
writer := &clog.GCPWriter{Logger: gcpLogger}

logger := slog.New(slog.NewJSONHandler(writer, &slog.HandlerOptions{
    ReplaceAttr: clog.GCPReplaceAttr,
}))
slog.SetDefault(logger)
```

---

## Sensitive Parameter Masking

In Gin, sensitive route parameters (such as tokens or credentials in URLs) can be masked in the `path` field to prevent leaking secrets:

```go
router.Use(clog.GinMiddleware(
    clog.WithSensitiveParams("token", "optInID", "apiKey"),
))
```

When a matching parameter is present, `path` logs the router template (e.g. `/invites/:token`) instead of the raw secret value.

---

## Testing Your Logs

Use the provided helpers in tests to inspect what fields were accumulated:

```go
ctx := clog.NewContext(context.Background())
clog.WithGroupField(ctx, "order", "id", "ord-123")

bag := clog.BagFromContext(ctx)
assert.Equal(t, "canonical-log-line", bag.Message())
```

---

## License

MIT License. See [LICENSE](LICENSE) for details.
