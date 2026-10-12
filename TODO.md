# TODO & Architecture Roadmap: Ports & Adapters (Hexagonal)

This document tracks technical debt, decoupling tasks, and architectural improvements needed to transform `canonical-logger` (`clog`) from an EventX-specific internal utility into a fully decoupled, extensible, framework-agnostic Go library using the **Ports & Adapters (Hexagonal Architecture)** pattern.

---

## 1. Core Decoupling (Pure Standard Library Core)

Currently, the root package `github.com/Shannor/canonical-logger` directly imports `github.com/gin-gonic/gin`, `github.com/oapi-codegen/gin-middleware`, and `cloud.google.com/go/logging`. This forces all consumers—regardless of what router or cloud provider they use—to pull in large external dependencies.

- [ ] **Zero-Dependency Core**:
  - The core package (`clog`) must depend **exclusively on the Go standard library** (`log/slog`, `context`, `sync`, `strings`, `time`).
  - Move framework-specific code (`gin`, `oapi-codegen`, `gcp/logging`) into dedicated adapter subpackages.
- [ ] **Pluggable Context Resolvers (`ContextExtractor`)**:
  - Currently, `BagFromContext` hardcodes checks for `gin.ContextKey` and `ginmiddleware.GetGinContext(ctx)`:
    ```go
    // Currently hardcoded in fieldbag.go:
    if ginCtx, ok := ctx.Value(gin.ContextKey).(*gin.Context); ...
    if ginCtx := ginmiddleware.GetGinContext(ctx); ...
    ```
  - Define a pluggable resolver interface:
    ```go
    type ContextBagResolver func(ctx context.Context) *FieldBag
    func RegisterContextResolver(resolver ContextBagResolver)
    ```
  - This allows the Gin adapter to register its context extraction without the core package ever importing Gin or OpenAPI packages.

---

## 2. Ports & Adapters: Middleware & Cloud Packages

Separate middlewares and vendor SDK integrations into standalone subpackages:

### HTTP Router Adapters
- [ ] **`middleware/gin`** (or `adapter/gin`):
  - Isolate `GinMiddleware` and Gin context binding into its own subpackage (`github.com/Shannor/canonical-logger/middleware/gin`).
  - Move `ginFieldBagKey` and Gin context extraction here.
  - Gin consumers import:
    ```go
    import cloggin "github.com/Shannor/canonical-logger/middleware/gin"
    router.Use(cloggin.Middleware())
    ```
- [ ] **`middleware/http`** (or `adapter/http`):
  - Standard `net/http` middleware (`func(http.Handler) http.Handler`) supporting Go 1.22+ `http.ServeMux`, Chi, Gorilla, etc.
- [ ] **Future Framework Adapters**:
  - `middleware/echo` for Labstack Echo.
  - `middleware/fiber` for Go Fiber.

### Cloud & Telemetry Adapters
- [ ] **`adapter/gcp`**:
  - Move `GCPWriter` and any dependencies on `cloud.google.com/go/logging` into `adapter/gcp`.
  - Non-GCP users should never have Google Cloud SDK or gRPC dependencies in their dependency tree.
- [ ] **`adapter/trace`**:
  - Extract trace extraction into pluggable trace providers:
    - W3C TraceContext (`traceparent`, `tracestate`).
    - Sentry headers (`sentry-trace`, `baggage`).
    - Native OpenTelemetry span extraction via `go.opentelemetry.io/otel/trace`.
- [ ] **Vendor Slog Formatters**:
  - `adapter/datadog`: Slog `ReplaceAttr` helper for Datadog's expected JSON fields (`dd.trace_id`, `status`).
  - `adapter/aws`: Slog `ReplaceAttr` helper for AWS CloudWatch JSON format.

---

## 3. Generalize EventX-Specific Hardcoding

Several default behaviors and constants in `clog` were inherited directly from EventX backend logic:

- [ ] **Remove Hardcoded Sensitive Route Parameters**:
  - In `middleware.go`, default sensitive parameters are currently hardcoded to:
    ```go
    sensitiveParams: []string{"token", "optInID"} // EventX capability tokens & opt-in IDs
    ```
  - Make `sensitiveParams` default to empty or standard universal secret names (e.g. `"token"`, `"apiKey"`, `"secret"`, `"password"`), and require application-specific route params (like `optInID`) to be supplied via `WithSensitiveParams(...)`.
- [ ] **Configurable Root HTTP Attributes**:
  - Currently root attributes are hardcoded to `status`, `method`, `path`, `ip`, `latency`, `user-agent`.
  - Allow consumers to configure:
    - Custom attribute keys (e.g. `http_status` vs `status`, `http.method` vs `method`).
    - Latency formatting (e.g. duration string `"142ms"` vs milliseconds `142` vs float seconds `0.142`).
    - Inclusion/exclusion of specific root headers (e.g. opt-in query parameter inclusion).
- [ ] **Customizable Severity Escalation**:
  - Currently, `clog` escalates to `WARN` on HTTP 4xx or `hasWarning()`, and `ERROR` on HTTP 5xx or `hasGroup("error")`.
  - Provide a pluggable `LevelEvaluator` hook:
    ```go
    type LevelEvaluator func(statusCode int, bag *FieldBag) slog.Level
    ```
    This allows applications that treat certain 4xx errors (e.g. 401 Unauthorized or 422 Unprocessable Entity) differently to customize log level escalation.

---

## 4. Documentation & Guideline Generalization

- [ ] **Update Documentation Examples**:
  - Replace EventX-specific domain terms (e.g. promoters, campaigns, attendees, tickets) across `README.md` and `AGENTS.md` with standard, domain-agnostic examples (e.g. users, orders, payments, notifications).
- [ ] **Architecture Guide**:
  - Document the Hexagonal Ports & Adapters boundary so new contributors understand where to put new adapters versus what belongs in core `clog`.

---

## 5. Performance & Advanced Features

- [ ] **`sync.Pool` for `FieldBag` Allocations**:
  - Evaluate using a `sync.Pool` for `FieldBag` struct instances in ultra-high-throughput services.
- [ ] **Canonical Log Validator for Tests**:
  - Add a testing test-harness helper (`clogtest.AssertGroup(t, buf, "orderService.checkout", expectedFields)`) to make validating canonical log output in unit/integration tests trivial.
