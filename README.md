# canonical-logger (`clog`)

[![Go Reference](https://pkg.go.dev/badge/github.com/Shannor/canonical-logger.svg)](https://pkg.go.dev/github.com/Shannor/canonical-logger)
[![Go Report Card](https://goreportcard.com/badge/github.com/Shannor/canonical-logger)](https://goreportcard.com/report/github.com/Shannor/canonical-logger)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

Structured canonical logging for Go, built on Go's standard library **`log/slog`**.

`canonical-logger` (`clog`) implements the **Canonical Log Line** pattern (pioneered by Stripe): emitting exactly **one rich, structured log line** per HTTP request or background task lifecycle. Rather than scattering noisy, disjointed log lines across handlers, services, and database layers, contextual metadata is accumulated in a thread-safe `FieldBag` across the execution lifecycle and emitted as a single structured JSON event.

For AI agents and automated coding workflows, see [AGENTS.md](AGENTS.md) for strict architectural rules, layer boundaries, and linting checklists.

---

## Highlights

- **Zero-Dependency Standard Library Core**: Built directly on Go's `log/slog` and `net/http` with **zero external runtime dependencies** in the root module.
- **Plug-and-Play Adapters (Ports & Adapters)**: Isolated sub-modules for third-party routers and cloud SDKs ([`adapter/gin`](adapter/gin), [`adapter/gcp`](adapter/gcp))—only pull in the dependencies you actually use.
- **Single Structured Event**: One log entry per request with complete operational, business, and error context.
- **Hierarchical Groups**: Organize context cleanly using nested JSON objects with dot-separated paths (e.g. `clog.WithGroupField(ctx, "db.getOrder", "rows", 1)`).
- **Strongly-Typed `slog.Attr` & `LogValuer` Support**: Fast-path `WithAttrs` and `WithGroupAttrs` helpers plus automatic `slog.LogValuer` evaluation for custom type serialization and PII redaction.
- **HTTP Middlewares**: Built-in standard `net/http` (`HTTPMiddleware`, compatible with Go 1.22+ `ServeMux`, Chi, Gorilla) and [Gin](adapter/gin) (`cloggin.Middleware`).
- **Trace Propagation**: Out-of-the-box W3C / Sentry trace ID extraction (`sentry-trace`, `baggage`) and fallback generation with response header injection.
- **Rich Boundary Error Tracking**: `SetResult` and `DeferResult` automatically record error details, unwrap root causes, extract `Code()` / `Category()` / `ErrorFields()` metadata via pluggable `ErrorFormatter`, and format outcome messages (`<event>-success`, `<event>-failure`).
- **Async & Background Support**:
  - `clog.Go`: Spawns detached goroutines with panic recovery, independent log sessions, logger propagation, and parent `FieldBag` correlation.
  - `clog.Func`: Synchronous tasks with automatic latency and line emission.
  - `clog.CronFunc`: Wraps scheduled jobs for cron runners.
- **Adaptive & Contextual Level Sampling**: `NewSamplingHandler` and `NewSamplingHandlerWithEvaluator` guarantee 100% capture of Warnings, Errors, Fatal, and Panics while sampling high-volume Info/Debug/Trace traffic by rate or custom context predicates.
- **Local Dev Console & Cloud Logging Ready**:
  - `NewConsoleHandler` for colorized, human-readable local terminal output.
  - `NewJSONHandler` and `GCPReplaceAttr` in core for zero-dependency structured JSON, plus [`adapter/gcp`](adapter/gcp) (`cloggcp.Writer`) for direct Google Cloud Logging SDK streaming.

---

## Installation

### Core Library (Zero External Dependencies)

```bash
go get github.com/Shannor/canonical-logger
```

Import either the root package or `pkg/clog`:

```go
import "github.com/Shannor/canonical-logger"
// or
import "github.com/Shannor/canonical-logger/pkg/clog"
```

### Optional Framework & Cloud Adapters

Adapters are published as isolated Go modules under [`adapter/`](adapter/README.md) so the core library never forces transitive framework or cloud SDK dependencies into your `go.mod`:

```bash
# Gin HTTP Middleware & OpenAPI Context Resolver
go get github.com/Shannor/canonical-logger/adapter/gin

# Google Cloud Logging SDK Writer
go get github.com/Shannor/canonical-logger/adapter/gcp
```

---

## Architectural Principles & Layer Responsibilities

Canonical logging relies on a clear separation of concerns across application layers. Each layer has specific responsibilities for what it enriches:

| Layer | Functions Permitted | Group Name | Purpose & Invariants |
| --- | --- | --- | --- |
| **Middleware** | `clog.HTTPMiddleware`, `cloggin.Middleware`, `clog.HTTPTraceMiddleware`, `cloggin.TraceID`, Auth middlewares | `actor` / root | Emits HTTP root fields (`status`, `method`, `path`, `ip`, `latency`, `user-agent`, `traceId`, `spanId`) and identity context (`actor`). |
| **Boundary (API / Webhooks)** | `SetResult()`, `DeferResult()`, `WithGroupFields()`, `WithGroupAttrs()` | `request` | Owns the canonical event name (`-success` / `-failure`), adds request input parameters & query filters (`request`). |
| **Service Layer** | `WithGroupFields()`, `WithGroupField()`, `WithGroupAttrs()` | `<serviceName>.<methodName>` | Records business execution details. **MUST NOT** set event names or touch the `"error"` group. Returns standard Go errors. |
| **DAO / DB Layer** | `WithGroupFields()`, `WithGroupField()`, `WithGroupAttrs()` | `<daoName>.<methodName>` or `db.<methodName>` | Records query metadata (`table`, `rowsReturned`, `durationMs`). **MUST NOT** set event names or touch `"error"`. Returns standard Go errors. |
| **Background / Cron** | `clog.Go()`, `clog.CronFunc()`, `clog.Func()` | `job` / `request` | Spawns isolated execution contexts with independent FieldBags, correlates with parent snapshots, and recovers from panics. |

---

## Field & Group Naming Conventions

Structured log engines (such as GCP Cloud Logging, Datadog, and Elasticsearch) query nested JSON via path expressions like `jsonPayload.<group>.<field>`. Consistent naming is critical.

### Group Naming Rules

- **Middleware**: Group MUST be `actor` (e.g. `requestorId`, `isAdmin`, `targetId`).
- **API Handlers**: Group MUST be `request` (e.g. `orderId`, `limit`, `offset`, `filters`).
- **Service Layer**: Group MUST follow `<serviceName>.<methodName>` (e.g. `orderService.checkout`, `paymentService.charge`).
- **DAO / DB Layer**: Group MUST follow `<daoName>.<methodName>` or `db.<methodName>` (e.g. `orderDAO.getByID`, `db.queryOrders`).
- **Error Group**: Group MUST be `error`. Populated **automatically** by `SetResult` / `DeferResult` on failure.

### Field Naming Rules

1. **camelCase Map Keys**: All keys in `clog.Fields` MUST use **camelCase** (e.g. `"userId"`, `"orderId"`, `"rowsReturned"`, `"durationMs"`).
2. **The `Id` Suffix**: Always use `Id` (not `ID`) in JSON string keys so search queries across entities are uniform (`"userId": userID`, `"orderId": orderID`). Go variable names remain standard Go idiom (`userID`, `orderID`).
3. **Always Use `clog.Fields` or `slog.Attr`**: Use the package type alias `clog.Fields{...}` or strongly-typed `slog.Attr` (`WithAttrs` / `WithGroupAttrs`). Never use bare `map[string]any` or `map[any]any`.
4. **Zero PII Policy**: **Never log PII** (phone numbers, email addresses, full names, physical addresses). Always log entity identifiers (UUIDs, DB IDs) or implement `slog.LogValuer` to redact sensitive fields automatically.

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
  "orderService": {
    "checkout": {
      "orderId": "ord_1001",
      "totalCents": 4500,
      "discountApplied": true
    }
  },
  "paymentService": {
    "charge": {
      "chargeId": "ch_3Mtw",
      "provider": "stripe",
      "status": "succeeded"
    }
  },
  "orderDAO": {
    "create": {
      "table": "orders",
      "rows": 1,
      "durationMs": 4
    }
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

## Result Tracking, Error Formatting & The Shadowing Gotcha

### How `SetResult` and `DeferResult` Work

Boundary layers call `SetResult` or `DeferResult` with the base event name:
- On success (`err == nil`): The message is set to `"<event>-success"`.
- On failure (`err != nil`): The message is set to `"<event>-failure"`, the canonical log escalates to `ERROR` level, and the `"error"` group is populated via `DefaultErrorFormatter` (or a custom `ErrorFormatter` configured with `clog.SetErrorFormatter`).

### Rich Error Metadata Extraction (`DefaultErrorFormatter`)

`DefaultErrorFormatter` automatically inspects wrapped error chains (`errors.Unwrap` / `errors.As`) and extracts:
- `"err"`: Full `err.Error()` string.
- `"cause"`: Root unwrapped error message (when `errors.Unwrap(err)` differs from the outer error).
- `"code"`: Extracted if any error in the chain implements `Code() string`.
- `"category"`: Extracted if any error in the chain implements `Category() string`.
- Custom fields: Merged if any error in the chain implements `ErrorFields() map[string]any`.

You can also register a custom formatter globally:

```go
clog.SetErrorFormatter(func(err error) clog.Fields {
    fields := clog.DefaultErrorFormatter(err)
    fields["service"] = "billing-api"
    return fields
})
```

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

### 1. With Gin (`adapter/gin`)

```go
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"

	"github.com/Shannor/canonical-logger"
	cloggin "github.com/Shannor/canonical-logger/adapter/gin"
	"github.com/gin-gonic/gin"
)

func main() {
	// Configure slog JSON handler with standard "message" key
	logger := slog.New(clog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	r := gin.New()

	// Register canonical logger and trace middleware from adapter/gin
	r.Use(cloggin.Middleware())
	r.Use(cloggin.TraceID())

	r.POST("/api/v1/orders", func(c *gin.Context) {
		ctx := c.Request.Context()

		var err error
		defer clog.DeferResult(ctx, "create-order", &err)

		// Enrich request context
		clog.WithGroupFields(ctx, "request", clog.Fields{
			"cartId":     "cart_99",
			"totalCents": 5000,
		})

		// Call business layer passing ctx
		err = createOrderService(ctx, "cart_99")
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	r.Run(":8080")
}

func createOrderService(ctx context.Context, cartID string) error {
	clog.WithGroupField(ctx, "orderService.create", "orderId", "ord-456")
	clog.WithGroupFields(ctx, "db.insertOrder", clog.Fields{
		"table":      "orders",
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
		clog.WithGroupField(ctx, "user", "userId", r.PathValue("id"))
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

### Adding Fields & Strongly-Typed `slog.Attr`

```go
// Add a flat root-level field
clog.WithField(ctx, "requestId", "req-123")

// Add multiple root-level fields
clog.WithFields(ctx, clog.Fields{
    "version": "1.0.0",
    "region":  "us-east-1",
})

// Add strongly-typed slog.Attr values at root
clog.WithAttrs(ctx,
    slog.String("region", "us-east-1"),
    slog.Int("attempt", 2),
)

// Add a single field to a nested group
clog.WithGroupField(ctx, "actor", "userId", "usr-abc")

// Add multiple fields to a group (supports dot-separated paths up to 3 levels deep)
clog.WithGroupFields(ctx, "db.getUser", clog.Fields{
    "queryDurationMs": 14,
    "rows":            1,
})

// Add strongly-typed slog.Attr values to a group
clog.WithGroupAttrs(ctx, "orderService.checkout",
    slog.String("orderId", "ord_123"),
    slog.Int64("durationMs", 18),
)

// Override the default canonical message ("canonical-log-line")
clog.WithMessage(ctx, "custom-event-message")
```

### Safe Redaction with `slog.LogValuer`

Any value stored in `FieldBag` that implements `slog.LogValuer` is resolved via `LogValue()` during emission, making it easy to prevent accidental PII or secret logging on domain structs:

```go
type Customer struct {
    ID    string
    Email string // PII - never log
}

func (c Customer) LogValue() slog.Value {
    return slog.GroupValue(slog.String("customerId", c.ID))
}

// Safely logs {"customer": {"customerId": "cust_123"}} without Email
clog.WithField(ctx, "customer", customer)
```

---

## Handlers, Extended Levels & Sampling

### Local Development Console Handler (`NewConsoleHandler`)

For local development, use `NewConsoleHandler` to print clean, colorized, human-readable canonical log lines instead of raw JSON blobs:

```go
handler := clog.NewConsoleHandler(os.Stdout, &clog.ConsoleHandlerOptions{
    Level:   clog.LevelDebug,
    NoColor: false,
})
slog.SetDefault(slog.New(handler))
```

### Extended Log Levels

In addition to standard `slog` levels (`LevelDebug`, `LevelInfo`, `LevelWarn`, `LevelError`), `clog` defines vendor-neutral extended levels rendered automatically by `NewJSONHandler` and `NewConsoleHandler`:
- `clog.LevelTrace` (`"TRACE"`)
- `clog.LevelFatal` (`"FATAL"`)
- `clog.LevelPanic` (`"PANIC"`)

### Adaptive & Contextual Level Sampling

Use `NewSamplingHandler` or `NewSamplingHandlerWithEvaluator` to wrap any `slog.Handler`. Warnings, Errors, Fatal, and Panics are **always retained at 100%**, while Info/Debug/Trace logs are sampled by rate or a contextual `SampleEvaluator`:

```go
baseHandler := clog.NewJSONHandler(os.Stdout, nil)

// Always log premium tenants at 100%; sample other Info/Debug traffic at 10%
samplingHandler := clog.NewSamplingHandlerWithEvaluator(
    baseHandler,
    0.10,
    func(ctx context.Context, bag *clog.FieldBag) bool {
        if bag != nil && bag.HasGroup("vip") {
            return true
        }
        return false
    },
)

slog.SetDefault(slog.New(samplingHandler))
```

---

## Background Tasks & Goroutines

### `clog.Go`: Detached Background Goroutine

Spawns a goroutine decoupled from the parent context's cancellation (`context.WithoutCancel`), with:
- Its own fresh `FieldBag` and inherited context `*slog.Logger`.
- A snapshot of the parent's `FieldBag` attached as a `"parent"` group for log correlation.
- Panic recovery logging with automatic stack error capture and canonical flush.

```go
clog.Go(ctx, "send-welcome-email", func(bgCtx context.Context) {
    var err error
    defer clog.DeferResult(bgCtx, "send-welcome-email", &err)

    err = emailService.Send(bgCtx, userID)
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
c.AddFunc("@hourly", clog.CronFunc("sync-subscriptions", func(ctx context.Context) {
    var err error
    defer clog.DeferResult(ctx, "sync-subscriptions", &err)

    err = syncService.Sync(ctx)
}))
```

---

## Google Cloud Logging Integration

### Option A: Standard JSON to Stdout (Cloud Run, GKE, Compute Engine)

GCP Cloud Logging automatically parses stdout JSON. Use `clog.GCPReplaceAttr` (included in the zero-dependency core) to map the level key to `"severity"` (with values `"DEBUG"`, `"INFO"`, `"WARNING"`, `"ERROR"`) and message to `"message"`:

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

### Option B: Google Cloud Logging SDK (`adapter/gcp`)

If using the GCP Cloud Logging Go SDK directly, install `github.com/Shannor/canonical-logger/adapter/gcp`:

```go
import (
    "log/slog"

    "cloud.google.com/go/logging"
    cloggcp "github.com/Shannor/canonical-logger/adapter/gcp"
)

client, err := logging.NewClient(ctx, "my-gcp-project-id")
if err != nil {
    panic(err)
}
defer client.Close()

gcpLogger := client.Logger("my-service-name")
writer := &cloggcp.Writer{Logger: gcpLogger}

logger := slog.New(slog.NewJSONHandler(writer, &slog.HandlerOptions{
    ReplaceAttr: cloggcp.ReplaceAttr,
}))
slog.SetDefault(logger)
```

---

## Sensitive Parameter Masking

In Gin, sensitive route parameters (such as tokens or credentials in URLs) can be masked in the `path` field to prevent leaking secrets:

```go
router.Use(cloggin.Middleware(
    cloggin.WithSensitiveParams("token", "apiKey"),
))
```

When a matching parameter is present, `path` logs the router template (e.g. `/invites/:token`) instead of the raw secret value.

---

## Testing Your Logs

Use the provided helpers in tests to inspect what fields were accumulated:

```go
ctx := clog.NewContext(context.Background())
clog.WithGroupField(ctx, "order", "orderId", "ord-123")

bag := clog.BagFromContext(ctx)
assert.Equal(t, "canonical-log-line", bag.Message())
```

---

## Architecture: Ports & Adapters (Multi-Module Repository)

`canonical-logger` uses a **Ports & Adapters (Hexagonal Architecture)** structured as a **Go Multi-Module Repository**:

- **Zero-Dependency Core (`github.com/Shannor/canonical-logger`)**: Depends exclusively on the Go standard library (`log/slog`, `context`, `net/http`). Domain services and DAOs only ever import `clog`.
- **Pluggable Ports**: The core package exposes `clog.RegisterContextResolver`, `(*FieldBag).HasGroup`, `(*FieldBag).HasWarning`, and `clog.ExtractTrace` so framework adapters can integrate without the core package ever importing third-party libraries.
- **Isolated Adapter Sub-Modules (`adapter/*`)**: Each third-party integration ([`adapter/gin`](adapter/gin), [`adapter/gcp`](adapter/gcp)) has its own `go.mod` so consumers only download dependencies for the adapters they explicitly install.
- **Lockstep Versioning (`Makefile`)**: Releases tag the root module (`vX.Y.Z`) and all adapter modules (`adapter/<name>/vX.Y.Z`) in lockstep using `make bump VERSION=vX.Y.Z` and `make tag VERSION=vX.Y.Z`.

For more details:
- **[`docs/architecture.md`](docs/architecture.md)**: Deep-dive into the Ports & Adapters architecture, module boundaries, and context resolution flow.
- **[`docs/adr/0001-ports-and-adapters-multi-module-architecture.md`](docs/adr/0001-ports-and-adapters-multi-module-architecture.md)**: Architectural decision record explaining why we chose the multi-module Ports & Adapters design.
- **[`adapter/README.md`](adapter/README.md)**: How `go.work` operates for local multi-module development, how to create new adapters, and how release tagging works.
- **[`TODO.md`](TODO.md)**: Project roadmap and upcoming features.

---

## License

MIT License. See [LICENSE](LICENSE) for details.
