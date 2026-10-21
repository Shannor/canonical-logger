# Building Custom Adapters, Middlewares & Writers (`docs/adapters/`)

`canonical-logger` (`clog`) ships as a **100% pure Go standard-library package** (`log/slog`, `context`, `net/http`) with zero external runtime dependencies and zero hardcoded framework or vendor headers.

Instead of bundling third-party router, RPC, OpenAPI, tracing, or cloud SDK subpackages in core, `clog` exposes small, composable **Ports** in the root package. You can wire any framework, tracer, or log sink into `clog` in ~20–40 lines of code inside your own application.

---

## 1. The Core Ports in `clog`

Every canonical logging integration uses the building blocks exported by `github.com/Shannor/canonical-logger`:

| Port / Function | Purpose |
| :--- | :--- |
| `clog.NewContext(ctx)` | Opens a new Unit of Work by creating a fresh `*clog.FieldBag` (inheriting any context `*slog.Logger`) and returning the enriched `context.Context`. |
| `clog.BagFromContext(ctx)` | Retrieves the active `*clog.FieldBag` from `ctx` (or via registered `ContextResolver` hooks). |
| `clog.RegisterContextResolver(fn)` | Registers a fallback `func(ctx context.Context) *clog.FieldBag` for frameworks that wrap or replace `context.Context`. |
| `bag.HasGroup("error")` / `bag.HasWarning()` / `bag.Message()` / `bag.Attrs()` | Inspects the finalized Unit of Work at the end of a request/task to determine log level and emit `[]slog.Attr`. |
| `clog.Run(ctx, event, fn)` / `clog.RunValue` / `clog.HTTPHandler` | Boundary wrappers that record the Unit of Work's outcome (`-success` vs `-failure`) from `fn`'s returned error (and auto-flush if no middleware opened a `FieldBag`). |
| `clog.SetErrorFormatter(fn)` | Configures how returned errors are extracted into the `"error"` group on failure. |

---

## 2. The 4-Step Middleware Lifecycle

When writing a transport middleware (for `net/http`, Gin, Echo, Fiber, gRPC, or a message consumer), every canonical middleware drives the same 4-step **Wide Event per Unit of Work** lifecycle (see [`docs/architecture.md`](../architecture.md)):

1. **Open Unit of Work**: Create an isolated `FieldBag` via `ctx := clog.NewContext(reqCtx)` and attach it to the request.
2. **Execute & Record Outcome**: Call the next handler in the chain, where handlers and services accumulate fields and boundary wrappers (`clog.HTTPHandler`, `clog.Run`, `clog.RunValue`) record `-success` or `-failure` from returned errors.
3. **Evaluate Level**: Escalate to `slog.LevelError` if status >= 500, transport errors exist, or `bag.HasGroup("error")` is true; escalate to `slog.LevelWarn` if status >= 400 or `bag.HasWarning()` is true.
4. **Emit Wide Event**: Call `clog.LoggerFromContext(ctx).LogAttrs(ctx, level, bag.Message(), attrs...)` combining root transport attributes with `bag.Attrs()...`.

*(Note: If you do not install a transport middleware, `clog.Run`, `clog.RunValue`, and `clog.HTTPHandler` automatically open a `FieldBag` and flush the canonical log line with `"job"` and `"latency"` when the boundary function returns.)*

---

## 3. Adapter & Middleware Recipes

Each recipe is self-contained in its own file so you can copy only what your service needs:

| Recipe | Description |
| :--- | :--- |
| **[Standard `net/http` Middleware (`nethttp.md`)](./nethttp.md)** | Canonical HTTP middleware for Go 1.22+ `http.ServeMux`, Chi, Gorilla Mux, and `clog.HTTPHandler`. |
| **[Gin Middleware & ContextResolver (`gin.md`)](./gin.md)** | Gin canonical logger middleware, sensitive route parameter masking, and `ContextResolver` registration. |
| **[`oapi-codegen` Strict Server (`oapi-codegen.md`)](./oapi-codegen.md)** | Automatic `operationId` outcome recording and error hooks for `oapi-codegen` strict servers (`net/http`, Chi, Gin). |
| **[gRPC Unary Interceptor (`grpc.md`)](./grpc.md)** | Canonical log line interceptor for `google.golang.org/grpc` unary servers. |
| **[Distributed Tracing (`tracing.md`)](./tracing.md)** | Trace extraction middlewares for W3C `traceparent`, OpenTelemetry spans, and Sentry `sentry-trace` / `baggage`. |
| **[Google Cloud Logging (`gcp-writer.md`)](./gcp-writer.md)** | Zero-dependency stdout JSON (`clog.GCPReplaceAttr`) and custom `cloud.google.com/go/logging` `io.Writer`. |
