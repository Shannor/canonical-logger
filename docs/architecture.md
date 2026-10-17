# `canonical-logger` (`clog`) Architecture Guide

This document describes the internal architecture of `canonical-logger`, its **Ports & Adapters (Hexagonal)** module boundaries, the `FieldBag` lifecycle, and how multi-module development and versioning operate.

---

## 1. Module Topology & Dependency Boundaries

`canonical-logger` is organized as a **Multi-Module Go Repository** so that the core library never forces third-party web frameworks, OpenAPI generators, or cloud vendor SDKs onto consumers.

```text
github.com/Shannor/canonical-logger (Root Module — Pure Go Standard Library)
├── clog.go                 # Extended log levels, SamplingHandler, SampleEvaluator, NewJSONHandler, GCPReplaceAttr
├── console_handler.go      # Colorized human-readable ConsoleHandler for local development
├── fieldbag.go             # Thread-safe FieldBag, ContextResolver port, WithField(s)/WithAttrs helpers
├── result.go               # SetResult, DeferResult, pluggable ErrorFormatter & DefaultErrorFormatter
├── background.go           # Detached goroutines (Go), sync tasks (Func), scheduled jobs (CronFunc)
├── middleware.go           # Standard net/http HTTPMiddleware
├── trace.go                # HTTPTraceMiddleware & exported ExtractTrace port
└── pkg/clog/               # Drop-in alias package re-exporting root symbols

github.com/Shannor/canonical-logger/adapter/gin (Isolated Sub-Module)
└── gin.go                  # Gin Middleware, TraceID, and Gin/OpenAPI ContextResolver registration

github.com/Shannor/canonical-logger/adapter/gcp (Isolated Sub-Module)
└── writer.go               # Google Cloud Logging SDK io.Writer (cloggcp.Writer) & ReplaceAttr
```

### Dependency Rule

- **Domain Services & DAOs** only import `github.com/Shannor/canonical-logger` (or `pkg/clog`). They interact strictly with `context.Context`.
- **Application Entrypoints (`main.go` / Router Wiring)** import the relevant adapter module (e.g. `adapter/gin`, `adapter/gcp`) to wire up middlewares and log sinks.

---

## 2. Core Ports & How Adapters Plug In

Because the core package does not import third-party frameworks, it exposes **Ports** that adapters implement or invoke:

### Port 1: `ContextResolver` & `RegisterContextResolver`

In standard Go `net/http` handlers and background tasks, `FieldBag` is stored directly in `context.Context` under an unexported struct key (`fieldBagCtxKey{}`). `BagFromContext(ctx)` retrieves it in O(1).

However, certain frameworks (such as Gin or `oapi-codegen` middleware wrappers) may replace `c.Request` with a new `context.Context` that only embeds `*gin.Context` under `gin.ContextKey`.

To support these frameworks without importing them in core:
1. Core exposes:
   ```go
   type ContextResolver func(ctx context.Context) *FieldBag
   func RegisterContextResolver(resolver ContextResolver)
   ```
2. `adapter/gin` stores the `*clog.FieldBag` in `gin.Context` (`c.Set("__clog_fieldbag__", bag)`) and registers a `ContextResolver` in `func init()`.
3. When `clog.BagFromContext(ctx)` is called:
   - **Fast Path**: Checks `ctx.Value(fieldBagCtxKey{})`.
   - **Fallback Path**: Iterates registered `ContextResolver` functions until one returns a non-nil `*FieldBag`.

### Port 2: `FieldBag` Inspection (`HasGroup`, `HasWarning`, `Attrs`, `Message`)

External router/protocol middlewares (HTTP, Gin, gRPC, message consumers) need to inspect the accumulated `FieldBag` at the end of a request to determine the final `slog.Level` and emit attributes:
- `bag.HasGroup("error")`: Escalates log severity to `slog.LevelError`.
- `bag.HasWarning()`: Escalates log severity to `slog.LevelWarn` when a `"warning"` or `"warnings"` key/group exists.
- `bag.Message()`: Returns the canonical event name (e.g. `"checkout-order-success"` or `"canonical-log-line"`).
- `bag.Attrs()`: Converts all root fields and nested groups into `[]slog.Attr` using zero-reflection fast paths for primitive types, `slog.Attr`, `slog.Value`, and `slog.LogValuer`.

### Port 3: `ExtractTrace`

`clog.ExtractTrace(sentryTrace, baggage)` parses incoming trace headers and falls back to generating a cryptographically random 32-character hex `traceId`, allowing any HTTP or RPC adapter to share trace extraction logic.

### Port 4: `ErrorFormatter` & `SetErrorFormatter`

Boundary layers call `clog.SetResult(ctx, "event-name", err)` or `defer clog.DeferResult(ctx, "event-name", &err)`. When `err != nil`, `clog` invokes the configured `ErrorFormatter` (`DefaultErrorFormatter` by default) to populate the `"error"` group with `"err"`, unwrapped root `"cause"`, `"code"`, `"category"`, and `ErrorFields()`.

---

## 3. Local Multi-Module Development (`go.work`)

Because `./go.mod`, `./adapter/gin/go.mod`, and `./adapter/gcp/go.mod` are separate Go modules, local development uses a Go Workspace (`go.work`):

- Run `make work-init` to generate a root `go.work` file linking `.`, `./adapter/gin`, and `./adapter/gcp`.
- While `go.work` exists locally, `go test` and IDE tooling resolve `github.com/Shannor/canonical-logger` directly to your working tree without requiring `replace` directives in `adapter/*/go.mod`.
- `go.work` and `go.work.sum` are git-ignored so committed `go.mod` files remain clean.

---

## 4. Lockstep Versioning & Release Workflow

Go resolves modules located in subdirectories using path-prefixed Git tags:
- Root module (`github.com/Shannor/canonical-logger`): tag `vX.Y.Z`
- Gin adapter (`github.com/Shannor/canonical-logger/adapter/gin`): tag `adapter/gin/vX.Y.Z`
- GCP adapter (`github.com/Shannor/canonical-logger/adapter/gcp`): tag `adapter/gcp/vX.Y.Z`

The root [`Makefile`](../Makefile) automates lockstep releases across all modules:

```bash
# 1. Point all adapter/*/go.mod files to the new core version
make bump VERSION=v0.3.0
make tidy

# 2. Run the full test suite (with race detector) across all modules
make test

# 3. Commit and create lockstep Git tags for root + all adapters
git commit -am "chore: release v0.3.0"
make tag VERSION=v0.3.0

# 4. Push all tags to origin
make push-tags VERSION=v0.3.0
```
