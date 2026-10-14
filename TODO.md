# TODO & Architecture Roadmap: EventX Decoupling & Agnostic Architecture

This document tracks technical debt, decoupling tasks, and architectural improvements needed to transform `canonical-logger` (`clog`) from an EventX-specific internal utility into a fully decoupled, extensible, protocol- and framework-agnostic Go library using the **Ports & Adapters (Hexagonal Architecture)** pattern.

---

## 1. Core Decoupling (Pure Standard Library Core)

Currently, the root package `github.com/Shannor/canonical-logger` directly imports `github.com/gin-gonic/gin`, `github.com/oapi-codegen/gin-middleware`, and `cloud.google.com/go/logging`. This forces all consumers—regardless of what router or cloud provider they use—to pull in large external dependencies and dozens of transitive gRPC and Google Cloud packages.

- [ ] **Zero-Dependency Core**:
  - The core package (`clog`) must depend **exclusively on the Go standard library** (`log/slog`, `context`, `sync`, `strings`, `time`).
  - Move framework-specific code (`gin`, `oapi-codegen`, `gcp/logging`) into dedicated adapter subpackages.
  - The root `go.mod` should have **zero external direct dependencies** (except test dependencies like `testify`).
- [ ] **Pluggable Context Resolvers (`ContextResolver`)**:
  - Currently, `BagFromContext` hardcodes checks for `gin.ContextKey` and `ginmiddleware.GetGinContext(ctx)`:
    ```go
    // Currently hardcoded in fieldbag.go:
    if ginCtx, ok := ctx.Value(gin.ContextKey).(*gin.Context); ...
    if ginCtx := ginmiddleware.GetGinContext(ctx); ...
    ```
  - Define a pluggable resolver interface in core:
    ```go
    type ContextResolver func(ctx context.Context) *FieldBag
    func RegisterContextResolver(resolver ContextResolver)
    ```
  - This allows the Gin and OpenAPI adapters to register their context extraction without the core package ever importing Gin or OpenAPI packages.
- [ ] **Deprecation & Phasing Out of `pkg/xlog`**:
  - `pkg/xlog` is a legacy compatibility shim for the original EventX `xlog` package.
  - Add explicit deprecation warnings, documentation, and a sunset schedule (e.g. remove in v2.0.0).
  - Ensure zero references to `xlog` remain in the core codebase outside of `pkg/xlog/xlog.go`.

---

## 2. Ingress & Protocol Agnosticism (Beyond HTTP & Gin)

EventX was designed around an HTTP monolith using Gin. In modern distributed systems, services process traffic across multiple ingress protocols: message queues, event brokers, gRPC, and standard HTTP.

### Event-Driven Architecture (EDA) & Message Consumer Canonical Lines
- [ ] **Message / Event Consumer Canonical Line Pattern**:
  - In event-driven architectures (Kafka, RabbitMQ, AWS SQS, Google Cloud Pub/Sub, NATS, Redis Streams, CloudEvents), message consumption requires its own canonical log line lifecycle.
  - Introduce a first-class consumer helper / decorator:
    ```go
    clog.Consume(ctx, clog.ConsumerMeta{
        Broker:        "kafka",
        Topic:         "orders.created",
        Partition:     2,
        Offset:        104928,
        MessageID:     "msg_abc123",
        ConsumerGroup: "inventory-sync",
    }, carrier, func(ctx context.Context) error { ... })
    ```
  - Accumulate processing details in `<serviceName>.<methodName>`, evaluate errors, and emit exactly one canonical line per consumed message.
- [ ] **Standardized Event/Message Metadata Group (`event` / `message`)**:
  - Provide a standardized group for asynchronous message metadata: `messageId`, `topic`, `queue`, `partition`, `offset`, `consumerGroup`, `correlationId`, `causationId`, `schemaVersion`.
- [ ] **Worker & Background Task Framework Adapters**:
  - Adapters/middlewares for popular Go task workers:
    - River (`adapter/worker/river`)
    - Asynq (`adapter/worker/asynq`)
    - Temporal workflow & activity interceptors (`adapter/temporal`)
    - Watermill message router middleware (`adapter/watermill`)

### gRPC Protocol Adapters
- [ ] **`adapter/grpc` (Unary & Stream Interceptors)**:
  - Provide server-side interceptors:
    ```go
    cloggrpc.UnaryServerInterceptor(opts...)
    cloggrpc.StreamServerInterceptor(opts...)
    ```
  - Extract trace context from gRPC incoming metadata (`metadata.MD`).
  - Map gRPC status codes (`codes.Code`) to canonical HTTP-equivalent status and log severity (`OK`, `NotFound`, `Internal`, `Unauthenticated`, etc.).
  - Record gRPC root attributes: `grpc.service`, `grpc.method`, `grpc.code`, `grpc.status`, `latency`, `peer.address`.

### HTTP Router Adapters
- [ ] **`adapter/gin` (or `middleware/gin`)**:
  - Isolate `GinMiddleware` and Gin context binding into its own subpackage (`github.com/Shannor/canonical-logger/adapter/gin`).
  - Move `ginFieldBagKey` and Gin context extraction here.
  - Gin consumers import:
    ```go
    import cloggin "github.com/Shannor/canonical-logger/adapter/gin"
    router.Use(cloggin.Middleware())
    ```
- [ ] **`adapter/http` (or `middleware/http`)**:
  - Standard `net/http` middleware (`func(http.Handler) http.Handler`) supporting Go 1.22+ `http.ServeMux`, Chi, Gorilla, etc.
- [ ] **Future Framework Adapters**:
  - `adapter/echo` for Labstack Echo (`echo.MiddlewareFunc`).
  - `adapter/fiber` for Go Fiber (`fiber.Handler`).

---

## 3. Distributed Tracing & Telemetry Agnosticism (Beyond Sentry)

EventX hardcoded distributed tracing exclusively around Sentry headers (`sentry-trace` and `baggage: sentry-trace_id`) and the response header `X-Trace-Id`.

- [ ] **W3C TraceContext First (`traceparent`, `tracestate`)**:
  - Support the universal W3C TraceContext standard (`traceparent`: `version-traceid-parentid-traceflags`) as the first-class default trace extraction mechanism.
- [ ] **Pluggable Carrier Abstraction (`Carrier` / `TextMapCarrier`)**:
  - Decouple trace propagation from `http.Header`.
  - Define a generic carrier interface:
    ```go
    type Carrier interface {
        Get(key string) string
        Set(key, value string)
    }
    ```
  - Provide built-in carrier adapters for `http.Header`, `metadata.MD` (gRPC), Kafka record headers, CloudEvents attributes, and AMQP headers.
- [ ] **Native OpenTelemetry Integration (`adapter/otel`)**:
  - When OpenTelemetry is initialized in the host application, extract trace and span directly from the active span context:
    ```go
    span := trace.SpanFromContext(ctx)
    if span.SpanContext().IsValid() {
        traceID = span.SpanContext().TraceID().String()
        spanID = span.SpanContext().SpanID().String()
    }
    ```
- [ ] **Multi-Vendor Trace Provider Fallback Chain**:
  - Provide a configurable trace extraction chain:
    1. Active OpenTelemetry SpanContext (`trace.SpanFromContext(ctx)`)
    2. W3C TraceContext (`traceparent` / `tracestate`)
    3. Sentry Headers (`sentry-trace` / `baggage`)
    4. AWS X-Ray (`X-Amzn-Trace-Id`)
    5. Google Cloud Trace (`X-Cloud-Trace-Context`)
    6. B3 / Zipkin (`X-B3-TraceId`, `X-B3-SpanId`)
    7. Custom request header (e.g. `X-Request-Id`, `X-Correlation-Id`)
    8. Cryptographic 32-char hex fallback generation
- [ ] **Configurable Response Trace Header**:
  - Allow customizing or disabling the response header:
    - Default: `X-Trace-Id` (legacy compatibility)
    - Options: `X-Request-Id`, `traceparent`, `X-Correlation-Id`, or `none` (no response header mutation).
- [ ] **Outgoing Client-Side Trace Injection**:
  - Provide helper to inject current trace context into outgoing downstream calls:
    ```go
    clog.InjectTrace(ctx, clog.HTTPHeaderCarrier(req.Header))
    ```

---

## 4. Field, Group & Schema Agnosticism (Beyond EventX `actor` & `request`)

EventX enforced strict group naming (`actor` with `requestorId`/`isAdmin`/`targetId`, and `request`) and strict camelCase `Id` string suffixes. While structured conventions are healthy, hardcoding them into the library prevents teams with existing telemetry standards from adopting `clog`.

- [ ] **Configurable Group Naming Schema & Aliasing**:
  - Allow applications to configure standard group names:
    - `actor` -> configurable to `identity`, `user`, `auth`, `principal`, `client`, or `session`.
    - `request` -> configurable to `payload`, `params`, `args`, or `input`.
  - Provide preset options so organizations can declare their schema centrally:
    ```go
    clog.SetSchemaConfig(clog.SchemaConfig{
        ActorGroup:   "identity",
        RequestGroup: "payload",
    })
    ```
- [ ] **First-Class Multi-Tenancy Support**:
  - Modern B2B SaaS microservices require tenant context.
  - Provide built-in helpers for multi-tenancy:
    ```go
    clog.WithTenant(ctx, tenantID)
    clog.WithTenantFields(ctx, clog.Fields{
        "tenantId":       tenantID,
        "organizationId": orgID,
        "workspaceId":    workspaceID,
    })
    ```
  - Emitted either at root or under a dedicated `tenant` group according to schema configuration.
- [ ] **Semantic Convention Presets**:
  - Provide ready-to-use attribute mapping presets:
    - **`PresetStandard`**: The default clean camelCase schema (`status`, `method`, `path`, `latency`, `traceId`, `spanId`).
    - **`PresetOpenTelemetry`**: OpenTelemetry Semantic Conventions (`http.response.status_code`, `http.request.method`, `network.client.ip`, `url.path`).
    - **`PresetECS`**: Elastic Common Schema (`http.response.status_code`, `client.ip`, `url.path`).
    - **`PresetEventX`**: Legacy EventX monolith compatibility preset.
- [ ] **Configurable Latency Representation (Numeric vs String)**:
  - Currently latency is hardcoded as a Go string duration (`time.Since(start).String()`, e.g. `"142ms"` or `"1.24s"`).
  - In analytics databases and log aggregators (Datadog, BigQuery, Elasticsearch, Grafana Loki, Snowflake), duration strings cannot be numerically averaged, charted in histograms, or alerted on (p50, p95, p99).
  - Allow configuring latency format:
    - `LatencyFormatDurationString` (e.g. `"142ms"`, default for backward compatibility)
    - `LatencyFormatMillisecondsFloat` (e.g. `142.34`)
    - `LatencyFormatMillisecondsInt` (e.g. `142`)
    - `LatencyFormatSecondsFloat` (e.g. `0.14234`)
- [ ] **Configurable Root HTTP Attributes**:
  - Allow consumers to configure:
    - Custom attribute keys (e.g. `http_status` vs `status`, `http.method` vs `method`).
    - Inclusion of optional root metadata: `host`, `scheme`, `bytesWritten`, `protocol` (`HTTP/1.1`, `HTTP/2`), `referrer`.

---

## 5. Outcome & Error Model Agnosticism (Beyond Message Mutation)

EventX mutated the canonical message string by appending `-success` or `-failure` (`SetResult(ctx, "checkout", err)` -> `message: "checkout-success"`).

- [ ] **Decoupled Outcome Strategies (`OutcomeStrategy`)**:
  - In platforms like Datadog, Honeycomb, and Grafana Loki, modifying the log message causes high cardinality in message values, which degrades message clustering, log pattern detection, and indexing.
  - Provide configurable outcome strategies:
    - **`StrategyStructuredOutcome` (Recommended for modern APMs)**:
      - Message remains static or equals the base event name (`message: "checkout"` or `message: "canonical-log-line"`).
      - Adds structured attributes: `event: "checkout"`, `outcome: "success" | "failure"`, or `status: "ok" | "error"`.
    - **`StrategyMessageSuffix` (EventX Legacy)**:
      - Suffixes message with `-success` / `-failure`.
    - **`StrategyOpenTelemetry`**:
      - Emits `status.code: "OK"` or `status.code: "ERROR"`.
- [ ] **Pluggable Error Extractor / Formatter**:
  - Currently `SetResult` only records `error: { "err": err.Error() }`.
  - Provide an `ErrorFormatter` hook to extract rich error metadata:
    ```go
    type ErrorFormatter func(err error) map[string]any
    ```
  - Standard formatters for:
    - Error code extraction (`err.Code()` or interface with `Code() string`).
    - Stack trace capture (e.g. `pkg/errors`, Go 1.24+ runtime traces).
    - HTTP / gRPC status code mapping.
    - Error classification (e.g. `category: "validation" | "transient" | "internal"`).
    - Root cause unwrapping (`errors.Unwrap` chain).

---

## 6. Severity & Level Escalation Policy Engine

Currently, level escalation rules are hardcoded and inconsistent between middlewares:
- `HTTPMiddleware` checks `statusCode >= 500` (ERROR) and `statusCode >= 400` (WARN).
- `GinMiddleware` only checks `len(c.Errors) > 0` (ERROR) and does not check HTTP status code at all.

- [ ] **Harmonize Gin & net/http Status Parity**:
  - Ensure `GinMiddleware` and `HTTPMiddleware` share consistent default escalation behavior on status codes.
- [ ] **Pluggable Severity Policy (`SeverityPolicy` / `LevelEvaluator`)**:
  - Provide a pluggable policy hook:
    ```go
    type SeverityPolicy func(status int, bag *FieldBag, err error) slog.Level
    ```
  - Allow services to customize escalation:
    - Treat expected client errors (e.g. 401 Unauthorized, 404 Not Found, 422 Unprocessable Entity) as `INFO` instead of `WARN`.
    - Treat client disconnections (HTTP 499 / `context.Canceled`) as `INFO`.
    - Allow custom escalation triggers for background tasks and message consumers.
- [ ] **Configurable Warning Keys**:
  - Currently `hasWarning()` looks strictly for keys `"warning"` and `"warnings"`.
  - Allow configuring custom warning keys (e.g. `"warn"`, `"warningMsg"`).

---

## 7. Security, PII & Comprehensive Secret Redaction Engine

In EventX, sensitive parameter masking only handled route path parameter names, and hardcoded EventX-specific parameters (`sensitiveParams: []string{"token", "optInID"}`).

- [ ] **Remove Hardcoded EventX Route Parameters**:
  - Remove `optInID` from default sensitive parameters.
  - Default `sensitiveParams` to standard secret keys (`"token"`, `"apiKey"`, `"secret"`, `"password"`, `"auth"`).
- [ ] **Multi-Layer Redaction Engine**:
  - **Query String Redaction**:
    - Automatically redact or strip sensitive query parameters from logged paths (e.g. `?token=...`, `?api_key=...`, `?signature=...`).
  - **Header Sanitization**:
    - Automatically sanitize sensitive headers if request headers are ever logged (`Authorization`, `Cookie`, `Set-Cookie`, `X-Api-Key`, `Proxy-Authorization`).
  - **FieldBag Key-Based Masking**:
    - Provide an automatic FieldBag sanitizer that masks values for keys matching sensitive patterns:
      `password`, `secret`, `cvv`, `creditCard`, `ssn`, `privateKey`, `accessToken`.
    - Replaces values with `"[REDACTED]"`.
  - **Pluggable Sanitizer Callback**:
    ```go
    type FieldSanitizer func(group, key string, value any) any
    ```

---

## 8. Observability Vendor & Cloud Adapters (Ports & Adapters)

Vendor SDKs must be isolated into standalone subpackages so consumers only import what they use.

- [ ] **`adapter/gcp`**:
  - Move `GCPWriter` and any dependencies on `cloud.google.com/go/logging` into `adapter/gcp`.
  - Move `GCPReplaceAttr` here.
  - Consumers on AWS, bare metal, or Kubernetes should never pull in GCP SDKs.
- [ ] **`adapter/aws`**:
  - AWS CloudWatch JSON formatter (`ReplaceAttr` helper).
  - AWS X-Ray trace extraction (`X-Amzn-Trace-Id`).
- [ ] **`adapter/datadog`**:
  - Datadog trace correlation helper: maps trace and span IDs to Datadog's expected 64-bit integer format (`dd.trace_id`, `dd.span_id`).
  - Unified Service Tagging injection (`dd.service`, `dd.env`, `dd.version`).
- [ ] **`adapter/otlp` (OpenTelemetry Logging Protocol)**:
  - Slog handler that streams canonical log events directly to an OpenTelemetry Collector via gRPC / HTTP OTLP.
- [ ] **`adapter/loki`**:
  - Grafana Loki attribute mapper (separating high-cardinality payload fields from low-cardinality stream labels).
- [ ] **`adapter/dev` (Local Developer Console Formatter)**:
  - Local terminal pretty-printer: colorized, compact, readable single-line or multi-line output for terminal development, replacing unformatted JSON blobs during local debugging.

---

## 9. Extensibility, Lifecycle Hooks & Testing Harness

- [ ] **Lifecycle Hooks (`OnBeforeEmit`, `OnEmit`)**:
  - Allow registering hooks executed right before the canonical log line is emitted:
    ```go
    type EmitHook func(ctx context.Context, bag *FieldBag)
    ```
  - Enables cross-cutting plugins to inject ambient environment attributes:
    - Kubernetes metadata: pod name, node name, namespace.
    - Cloud metadata: AWS region, GCP project ID, availability zone.
    - Application build info: Git commit SHA, service version, build timestamp.
    - Runtime performance metrics: goroutine count, memory allocations (`runtime.MemStats`).
- [x] **Dynamic Sampling Evaluator Hook**:
  - Extend `LevelSampler` to support contextual sampling logic:
    ```go
    type SampleEvaluator func(ctx context.Context, bag *FieldBag) bool
    ```
  - Allows sampling by tenant, route, status code, or customer tier (e.g. 100% logging for premium tier, 1% for health checks).
- [ ] **First-Class Testing Harness (`clogtest`)**:
  - Create a dedicated test harness package (`github.com/Shannor/canonical-logger/pkg/clogtest`):
    ```go
    rec := clogtest.NewRecorder()
    ctx := clog.NewContextWithLogger(ctx, rec.Logger())

    // Run business logic...

    rec.AssertCanonicalLine(t)
    rec.AssertGroup(t, "orderService.checkout", clog.Fields{
        "orderId": "ord_123",
    })
    rec.AssertOutcome(t, "checkout", clogtest.OutcomeSuccess)
    ```
  - Eliminates boilerplate `bytes.Buffer` setup and manual JSON unmarshaling in unit and integration tests.
- [ ] **`sync.Pool` for High-Throughput Allocations**:
  - Benchmark and implement `sync.Pool` for `FieldBag` struct instances and nested map allocators in high-throughput environments.

---

## 10. Documentation, Guidelines & Deprecation Path

- [ ] **Purge EventX Domain Language**:
  - Replace domain-specific terms (`promoters`, `campaigns`, `attendees`, `tickets`, `optInID`) across `README.md`, `AGENTS.md`, and code comments with universal domain-agnostic examples (`users`, `orders`, `payments`, `notifications`, `subscriptions`).
- [ ] **Update `AGENTS.md` for Agnostic Architecture**:
  - Generalize the AI Agent instructions to emphasize:
    - Protocol agnosticism (HTTP, gRPC, message consumers).
    - Configurable group schemas (supporting `identity` / `tenant` alongside `actor`).
    - Standard W3C TraceContext conventions.
- [ ] **Ports & Adapters Architecture Guide**:
  - Author an `ARCHITECTURE.md` explaining the boundary between:
    - Pure stdlib core (`github.com/Shannor/canonical-logger`)
    - Router & protocol adapters (`adapter/gin`, `adapter/http`, `adapter/grpc`)
    - Telemetry & cloud adapters (`adapter/otel`, `adapter/gcp`, `adapter/aws`, `adapter/datadog`)
    - Test utilities (`pkg/clogtest`)
- [ ] **Migration Guide (`xlog` -> `clog`)**:
  - Provide a clear step-by-step guide for migrating legacy applications from `xlog` to `clog`.
