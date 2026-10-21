# canonical-logger (`clog`)

[![Go Reference](https://pkg.go.dev/badge/github.com/Shannor/canonical-logger.svg)](https://pkg.go.dev/github.com/Shannor/canonical-logger)
[![Go Report Card](https://goreportcard.com/badge/github.com/Shannor/canonical-logger)](https://goreportcard.com/report/github.com/Shannor/canonical-logger)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

Structured canonical logging for Go, built on Go's standard library **`log/slog`**.

`canonical-logger` (`clog`) implements the **Canonical Log Line / Wide Event** pattern (pioneered by Stripe and Honeycomb): emitting exactly **one rich, structured wide event per Unit of Work** (an HTTP request, RPC call, consumed message, or background job). Rather than scattering noisy, disjointed `log.Info()` side effects across handlers, services, and database layers, each unit of work opens an isolated `FieldBag` on `context.Context`, accumulates domain and operational metadata as execution traverses the stack, records its outcome (`<event>-success` vs `<event>-failure`) from the boundary function's returned `error`, and flushes a single structured JSON event when the boundary exits.

For AI agents and automated coding workflows, see [AGENTS.md](AGENTS.md) for strict architectural rules, layer boundaries, and linting checklists.

---

## Highlights

- **100% Zero-Dependency Standard Library Core**: Built directly on Go's `log/slog`, `context`, and `net/http` with **zero external runtime dependencies** in `go.mod`.
- **Composable Ports for Any Framework**: Exposes `RegisterContextResolver`, `HasGroup`, `HasWarning`, `ExtractTrace`, and `SetErrorFormatter` so you can wire any third-party router (Gin, Echo, Fiber), OpenAPI generator (`oapi-codegen`), RPC server (gRPC), or cloud SDK (`io.Writer`) in ~25 lines of code (see [Building Custom Adapters & Writers (`docs/adapters.md`)](docs/adapters.md)).
- **Single Structured Event**: One log entry per request with complete operational, business, and error context.
- **Hierarchical Groups**: Organize context cleanly using nested JSON objects with dot-separated paths (e.g. `clog.WithGroupField(ctx, "db.getOrder", "rows", 1)`).
- **Strongly-Typed `slog.Attr` & `LogValuer` Support**: Fast-path `WithAttrs` and `WithGroupAttrs` helpers plus automatic `slog.LogValuer` evaluation for custom type serialization and PII redaction.
- **Standard `net/http` Middlewares**: Built-in `HTTPMiddleware` and `HTTPTraceMiddleware` compatible with Go 1.22+ `http.ServeMux`, Chi, Gorilla Mux, and any `http.Handler`.
- **Shadow-Proof Unit-of-Work Result Tracking**: Outcomes are derived from return values via `clog.Run`, `clog.RunValue`, `clog.RunFunc`, `clog.HTTPHandler`, and `clog.Go`—ergonomically shaped like `errgroup.Go` or `db.RunInTx(ctx, func(ctx) error)`. Returning `nil` records `<event>-success`; returning an error or panicking records `<event>-failure` while preserving every field accumulated before the error, so `:=` shadowing can never log a false success. Failures are sticky.
- **Rich Boundary Error Tracking**: Failures automatically record error details, unwrap root causes, extract `Code()` / `Category()` / `ErrorFields()` metadata via pluggable `ErrorFormatter`, and format outcome messages (`<event>-success`, `<event>-failure`).
- **Async & Background Support**:
  - `clog.Go`: Spawns detached goroutines with panic recovery, independent log sessions, logger propagation, and parent `FieldBag` correlation.
  - `clog.Run`: Synchronous unit-of-work boundary with automatic `FieldBag` creation and line emission when no middleware is active.
  - `clog.RunFunc`: Wraps a `func(ctx) error` into a zero-argument `func()` for cron schedulers and callbacks.
- **Adaptive & Contextual Level Sampling**: `NewSamplingHandler` and `NewSamplingHandlerWithEvaluator` guarantee 100% capture of Warnings, Errors, Fatal, and Panics while sampling high-volume Info/Debug/Trace traffic by rate or custom context predicates.
- **Local Dev Console & Cloud Logging Ready**:
  - `NewConsoleHandler` for colorized, human-readable local terminal output.
  - `NewJSONHandler` and `GCPReplaceAttr` in core for zero-dependency structured JSON.

---

## Installation

```bash
go get github.com/Shannor/canonical-logger
```

```go
import "github.com/Shannor/canonical-logger"
```

---

## Architectural Principles & Layer Responsibilities

### One Wide Event per Unit of Work

`clog` scopes every canonical log line to a single **Unit of Work** across four phases:

1. **Open Unit of Work (`NewContext`)**: Transport middleware (`HTTPMiddleware`, Gin/gRPC interceptors) or a boundary runner (`Run`, `RunFunc`, `Go`) attaches a fresh, thread-safe `FieldBag` to `context.Context`.
2. **Accumulate (`WithGroupFields` / `WithAttrs`)**: Handlers, services, and DAOs enrich their respective namespace groups as work progresses, without emitting premature log lines or passing logger instances.
3. **Record Outcome (`Run` / `RunValue` / `HTTPHandler`)**: The boundary wrapper captures the function's returned `error`: `nil` records `<event>-success`, while a non-nil error (or panic) records `<event>-failure`, extracts structured error details via `ErrorFormatter`, and marks the failure as sticky so outer wrappers cannot downgrade it—all while preserving every field accumulated before the failure.
4. **Emit (`LogAttrs` at Boundary Exit)**: When the outermost middleware or `Run` / `RunFunc` / `Go` boundary finishes, it inspects the finalized `FieldBag` and emits one canonical wide event.

### When to Use `clog` vs. Standard `log/slog`

`clog` intentionally does **not** expose point-logging functions like `clog.Info()` or `clog.Error()`. Omitting ad-hoc emit helpers ensures services and DAOs cannot accidentally drift back into scattering multiple log lines per request.

Instead, choose the right tool based on whether you are inside a **Unit of Work**:

| Scenario | Example | What to Use |
| :--- | :--- | :--- |
| **1. Process lifecycle event** (no Unit of Work) | `"server listening on :8080"`, `"SIGTERM received, draining"`, `"loaded config"` | **Use standard `slog.Info` / `slog.Error` directly.** Because `slog.SetDefault` is configured with `clog.NewJSONHandler` or `clog.NewConsoleHandler` at startup, standalone `slog` calls automatically share the same JSON schema (`"message"`, `"severity"`) and console formatting. |
| **2. One-off synchronous operation** | Running DB migrations at startup, seeding data, or executing a CLI command | **Use `clog.Run(ctx, "run-migrations", func(ctx) error { ... })`.** Opens a `FieldBag` and emits one self-contained wide event with `latency`, `-success` / `-failure`, and any fields enriched by downstream DAOs/services. |
| **3. Inside an active request or task** | `"cache miss"`, `"rate limit near threshold"`, `"retrying provider"` | **Do not emit a separate log line.** Enrich the active Unit of Work via `clog.WithGroupField(ctx, "billingService.charge", "cacheHit", false)` (or add a `"warning"` field to escalate the wide event to `WARN`). |

### Layer Ownership

Canonical logging relies on a clear separation of concerns across application layers. Each layer has specific responsibilities for what it enriches:

| Layer | Functions Permitted | Group Name | Purpose & Invariants |
| --- | --- | --- | --- |
| **Middleware** | `clog.HTTPMiddleware`, `clog.HTTPTraceMiddleware`, Custom router/Auth middlewares | `actor` / root | Emits HTTP root fields (`status`, `method`, `path`, `ip`, `latency`, `user-agent`, `traceId`, `spanId`) and identity context (`actor`). |
| **Boundary (API / Webhooks)** | `HTTPHandler()`, `Run()`, `RunValue()`, `WithGroupFields()`, `WithGroupAttrs()` | `request` | Owns the canonical event name (`-success` / `-failure`), adds request input parameters & query filters (`request`). |
| **Service Layer** | `WithGroupFields()`, `WithGroupField()`, `WithGroupAttrs()` | `<serviceName>.<methodName>` | Records business execution details. **MUST NOT** set event names or touch the `"error"` group. Returns standard Go errors. |
| **DAO / DB Layer** | `WithGroupFields()`, `WithGroupField()`, `WithGroupAttrs()` | `<daoName>.<methodName>` or `db.<methodName>` | Records query metadata (`table`, `rowsReturned`, `durationMs`). **MUST NOT** set event names or touch `"error"`. Returns standard Go errors. |
| **Background / Cron** | `clog.Go()`, `clog.RunFunc()`, `clog.Run()` | `job` / `request` | Spawns isolated execution contexts with independent FieldBags, correlates with parent snapshots, and recovers from panics. |

---

## Field & Group Naming Conventions

Structured log engines (such as GCP Cloud Logging, Datadog, and Elasticsearch) query nested JSON via path expressions like `jsonPayload.<group>.<field>`. Consistent naming is critical.

### Group Naming Rules

- **Middleware**: Group MUST be `actor` (e.g. `requestorId`, `isAdmin`, `targetId`).
- **API Handlers**: Group MUST be `request` (e.g. `orderId`, `limit`, `offset`, `filters`).
- **Service Layer**: Group MUST follow `<serviceName>.<methodName>` (e.g. `orderService.checkout`, `paymentService.charge`).
- **DAO / DB Layer**: Group MUST follow `<daoName>.<methodName>` or `db.<methodName>` (e.g. `orderDAO.getByID`, `db.queryOrders`).
- **Error Group**: Group MUST be `error`. Populated **automatically** when a `Run`-based wrapper returns an error.

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

The handler is wrapped with `clog.HTTPHandler`, which records the returned error as the outcome, and tags the `request` group with input parameters:

```go
func (api OrderAPI) Checkout(w http.ResponseWriter, r *http.Request) error {
    ctx := r.Context()

    var req CheckoutRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return err
    }

    // Enrich request group
    clog.WithGroupFields(ctx, "request", clog.Fields{
        "cartId":    req.CartID,
        "currency":  req.Currency,
        "itemCount": len(req.Items),
    })

    order, err := api.orderService.Checkout(ctx, req)
    if err != nil {
        http.Error(w, "internal error", http.StatusInternalServerError)
        return err
    }

    w.Header().Set("Content-Type", "application/json")
    return json.NewEncoder(w).Encode(order)
}

// Registration:
mux.Handle("POST /checkout", clog.HTTPHandler("checkout-order", api.Checkout))
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

## Result Tracking & Error Formatting

### How Results Are Recorded

Boundary layers wrap their unit of work in `clog.Run` (or a wrapper built on it: `HTTPHandler`, `RunValue`, `RunFunc`, `Go`). The **returned error** determines the outcome of the wide event:
- On success (`err == nil`): The message is set to `"<event>-success"`.
- On failure (`err != nil` or panic): The message is set to `"<event>-failure"`, the canonical log escalates to `ERROR` level, and the `"error"` group is populated via `DefaultErrorFormatter` (or a custom `ErrorFormatter` configured with `clog.SetErrorFormatter`), preserving all fields accumulated across earlier steps.

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

### Result Wrappers

There is no `SetResult` / `DeferResult`: an outcome can only come from a function's **return value**, never from a captured `err` variable. A `:=` that shadows `err` therefore can't produce a false success (see [ADR-0002](docs/adr/0002-return-value-result-flow.md)).

| Boundary | Use |
| --- | --- |
| `net/http`, ServeMux, Chi | `clog.HTTPHandler("event", func(w, r) error)` |
| Gin / Echo / webhook / consumer / startup task | `err := clog.Run(ctx, "event", func(ctx) error)` |
| gRPC / RPC returning a value | `return clog.RunValue(ctx, "event", func(ctx) (T, error))` |
| Goroutine / zero-arg closure (cron, callbacks) | `clog.Go`, `clog.RunFunc` (the task name is the event name) |

```go
// net/http: the returned error IS the result.
mux.Handle("POST /actions", clog.HTTPHandler("handle-action", func(w http.ResponseWriter, r *http.Request) error {
    if condition {
        data, err := api.service.Process(r.Context()) // := is harmless
        if err != nil {
            http.Error(w, "internal error", http.StatusInternalServerError)
            return err // -> handle-action-failure
        }
        return json.NewEncoder(w).Encode(data)
    }
    return nil
}))

// Gin (or any framework): wrap the body with clog.Run.
func (api API) Create(c *gin.Context) {
    _ = clog.Run(c.Request.Context(), "create-campaign", func(ctx context.Context) error {
        var req CreateCampaignRequest
        if err := c.ShouldBindJSON(&req); err != nil {
            c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
            return err
        }
        campaign, err := api.campaignService.Create(ctx, req)
        if err != nil {
            c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
            return err
        }
        c.JSON(http.StatusOK, campaign)
        return nil
    })
}

// gRPC: RunValue passes the value and error straight through.
func (s Server) GetUser(ctx context.Context, req *pb.GetUserRequest) (*pb.User, error) {
    return clog.RunValue(ctx, "get-user", func(ctx context.Context) (*pb.User, error) {
        return s.users.Get(ctx, req.GetId())
    })
}
```

**Semantics:**
- **Return value is the outcome**: `nil` -> `<event>-success`; non-nil -> `<event>-failure` + `"error"` group + `ERROR` level.
- **Panics**: recorded as `<event>-failure`, then re-panicked so upstream recovery is unchanged (`clog.Go` recovers them).
- **Sticky failure**: once a failure is recorded (by a nested `Run`) or an `"error"` group is attached (e.g. by a router adapter), a later `nil` result does not downgrade the line to `-success`.
- **Nesting**: the outermost wrapper owns the event name.
- **Handlers write their own responses**: `clog.HTTPHandler` only records the outcome. Returning an error without writing a response still sends HTTP 200.

---

## Quick Start

### 1. Standard `net/http` / Go 1.22+ `ServeMux` / Chi (Built-In)

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

	mux.Handle("GET /users/{id}", clog.HTTPHandler("get-user", func(w http.ResponseWriter, r *http.Request) error {
		clog.WithGroupField(r.Context(), "request", "userId", r.PathValue("id"))
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte(`{"status":"ok"}`))
		return err
	}))

	// Wrap handler with clog HTTPMiddleware and HTTPTraceMiddleware
	handler := clog.HTTPMiddleware(clog.HTTPTraceMiddleware(mux))
	_ = http.ListenAndServe(":8080", handler)
}
```

### 2. Custom Frameworks (Gin, Echo, OpenAPI `oapi-codegen`, gRPC)

To keep `canonical-logger` 100% dependency-free, third-party router middlewares and `ContextResolver` hooks are written in your application using `clog`'s exported Ports (`NewContext`, `BagFromContext`, `RegisterContextResolver`, `HasGroup`, `HasWarning`, `ExtractTrace`).

See **[Building Custom Adapters, Middlewares & Writers (`docs/adapters.md`)](docs/adapters.md)** for ready-to-copy recipes for:
- **Gin** (`CanonicalLogger` middleware, `TraceID`, sensitive route parameter masking, and `gin.ContextKey` resolver)
- **OpenAPI `oapi-codegen`** (strict-server middleware that records every operation's outcome by operationId, plus request validation/binding error hooks, and a `ContextResolver` for `gin-middleware`)
- **gRPC** (`UnaryServerInterceptor`)
- **Google Cloud Logging SDK** (`GCPWriter`)

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
clog.Go(ctx, "send-welcome-email", func(bgCtx context.Context) error {
    return emailService.Send(bgCtx, userID) // -> send-welcome-email-success / -failure
})
```

### `clog.Run`: Synchronous Task

When called with a context that does not already carry a `FieldBag` (no outer HTTP/RPC middleware), `clog.Run` opens a `FieldBag` and automatically flushes the canonical log line when `fn` returns:

```go
if err := clog.Run(ctx, "startup-migrations", runMigrations); err != nil {
    os.Exit(1)
}
```

### `clog.RunFunc`: Zero-Argument Closure (Cron / Callbacks)

Wraps a `func(ctx context.Context) error` into a `func()` for cron schedulers (such as `robfig/cron`) or callbacks:

```go
c := cron.New()
c.AddFunc("@hourly", clog.RunFunc("sync-subscriptions", syncService.Sync))
```

---

## Google Cloud Logging Integration

GCP Cloud Logging automatically parses stdout JSON on Cloud Run, GKE, and Compute Engine. Use `clog.GCPReplaceAttr` (included in the zero-dependency core) to map the level key to `"severity"` (`"DEBUG"`, `"INFO"`, `"WARNING"`, `"ERROR"`) and message to `"message"`:

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

*(If your application streams logs directly via the `cloud.google.com/go/logging` SDK rather than stdout, see the `GCPWriter` recipe in [`docs/adapters.md`](docs/adapters.md#option-b-custom-google-cloud-logging-sdk-iowriter)).*

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

## Architecture & Documentation

`canonical-logger` combines a **Wide Event per Unit of Work** lifecycle with a **Zero-Dependency Ports & Adapters** architecture:
- **[`docs/architecture.md`](docs/architecture.md)**: Deep-dive into the Wide Event / Unit of Work lifecycle, `FieldBag` internals, core Ports, and context resolution flow.
- **[`docs/adapters.md`](docs/adapters.md)**: Step-by-step guide and copy-pasteable recipes for custom middlewares (Gin, gRPC), `ContextResolver` hooks (`gin`, `oapi-codegen`), and custom `io.Writer` sinks.
- **[`docs/adr/0001-zero-dependency-core-and-userland-adapters.md`](docs/adr/0001-zero-dependency-core-and-userland-adapters.md)**: Architectural Decision Record explaining why `clog` ships zero third-party adapters in-repo in favor of composable Ports and userland recipes.
- **[`docs/adr/0002-return-value-result-flow.md`](docs/adr/0002-return-value-result-flow.md)**: Architectural Decision Record explaining why unit-of-work outcomes are derived exclusively from return values (`Run`, `RunValue`, `HTTPHandler`).
- **[`TODO.md`](TODO.md)**: Project roadmap and upcoming features.

---

## License

MIT License. See [LICENSE](LICENSE) for details.
