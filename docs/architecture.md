# `canonical-logger` (`clog`) Architecture Guide

This document describes the internal architecture of `canonical-logger`, its **Ports & Adapters** design, and the `FieldBag` lifecycle.

---

## 1. Module Topology & Zero-Dependency Philosophy

`canonical-logger` is a **100% pure Go standard-library module** (`log/slog`, `context`, `net/http`). It never imports third-party web routers, OpenAPI generators, or cloud vendor SDKs.

```text
github.com/Shannor/canonical-logger (Pure Go Standard Library)
├── clog.go                 # Extended log levels, SamplingHandler, SampleEvaluator, NewJSONHandler, GCPReplaceAttr
├── console_handler.go      # Colorized human-readable ConsoleHandler for local development
├── fieldbag.go             # Thread-safe FieldBag, ContextResolver port, WithField(s)/WithAttrs helpers
├── result.go               # SetResult, DeferResult, pluggable ErrorFormatter & DefaultErrorFormatter
├── background.go           # Detached goroutines (Go), sync tasks (Func), scheduled jobs (CronFunc)
├── middleware.go           # Standard net/http HTTPMiddleware
├── trace.go                # HTTPTraceMiddleware & exported ExtractTrace port
└── pkg/clog/               # Drop-in alias package re-exporting root symbols
```

---

## 2. Core Ports & How Userland Adapters Plug In

Because the core package does not import third-party frameworks, it exposes **Ports** that userland middlewares, context resolvers, and log writers use to integrate cleanly (see [`docs/adapters.md`](./adapters.md) for complete copy-pasteable recipes):

### Port 1: `ContextResolver` & `RegisterContextResolver`

In standard Go `net/http` handlers and background tasks, `FieldBag` is stored directly in `context.Context` under an unexported struct key (`fieldBagCtxKey{}`). `BagFromContext(ctx)` retrieves it in O(1).

However, certain frameworks (such as Gin or `oapi-codegen` middleware wrappers) may replace `c.Request` with a new `context.Context` that only embeds the framework's context under a custom key.

To support these frameworks without importing them in core:
1. Core exposes:
   ```go
   type ContextResolver func(ctx context.Context) *FieldBag
   func RegisterContextResolver(resolver ContextResolver)
   ```
2. Your application's middleware stores the `*clog.FieldBag` in the framework context and registers a `ContextResolver` at startup (`func init()`).
3. When `clog.BagFromContext(ctx)` is called:
   - **Fast Path**: Checks `ctx.Value(fieldBagCtxKey{})`.
   - **Fallback Path**: Iterates registered `ContextResolver` functions until one returns a non-nil `*FieldBag`.

### Port 2: `FieldBag` Inspection (`HasGroup`, `HasWarning`, `Attrs`, `Message`)

Custom router and RPC middlewares (Gin, Echo, Fiber, gRPC, message consumers) inspect the accumulated `FieldBag` at the end of a request/task to determine the final `slog.Level` and emit attributes:
- `bag.HasGroup("error")`: Escalates log severity to `slog.LevelError`.
- `bag.HasWarning()`: Escalates log severity to `slog.LevelWarn` when a `"warning"` or `"warnings"` key/group exists.
- `bag.Message()`: Returns the canonical event name (e.g. `"checkout-order-success"` or `"canonical-log-line"`).
- `bag.Attrs()`: Converts all root fields and nested groups into `[]slog.Attr` using zero-reflection fast paths for primitive types, `slog.Attr`, `slog.Value`, and `slog.LogValuer`.

### Port 3: `ExtractTrace`

`clog.ExtractTrace(sentryTrace, baggage)` parses incoming trace headers and falls back to generating a cryptographically random 32-character hex `traceId`, allowing any HTTP or RPC middleware to share trace extraction logic.

### Port 4: `ErrorFormatter` & `SetErrorFormatter`

Boundary layers call `clog.SetResult(ctx, "event-name", err)` or `defer clog.DeferResult(ctx, "event-name", &err)`. When `err != nil`, `clog` invokes the configured `ErrorFormatter` (`DefaultErrorFormatter` by default) to populate the `"error"` group with `"err"`, unwrapped root `"cause"`, `"code"`, `"category"`, and `ErrorFields()`.
