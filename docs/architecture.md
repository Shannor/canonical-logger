# `canonical-logger` (`clog`) Architecture Guide

This document describes the internal architecture of `canonical-logger`, its **Canonical Log Line / Wide Event per Unit of Work** lifecycle, and its **Ports & Adapters** design.

---

## 1. Canonical Log Line / Wide Event per Unit of Work

Traditional application logging treats logging as an **immediate side effect**: handlers, services, and DAOs call `log.Info(...)` or `log.Error(...)` as they execute, scattering a single operation's context across dozens of narrow, disconnected log lines.

`canonical-logger` (`clog`) instead implements the **Canonical Log Line / Wide Event** pattern scoped to a **Unit of Work**:
- Every HTTP request, RPC call, consumed message, cron execution, or background job is a single **Unit of Work** backed by an isolated `*clog.FieldBag` on `context.Context`.
- Domain and infrastructure layers enrich the active `FieldBag` as execution traverses the stack, without emitting I/O side effects or knowing how the event will be transported.
- Boundary wrappers (`clog.Run`, `clog.RunValue`, `clog.RunFunc`, `clog.HTTPHandler`, `clog.Go`) wrap the unit of work's execution (ergonomically shaped like `errgroup.Go` or `db.RunInTx(ctx, func(ctx) error)`) and record its outcome (`<event>-success` vs `<event>-failure`) strictly from the returned `error`.
- Unlike a database transaction—which discards state on failure—a failed Unit of Work **preserves every field accumulated up to the point of failure** and guarantees 100% retention under `SamplingHandler`.
- When the outermost boundary exits, the transport middleware or task runner emits the finalized `FieldBag` as a single wide `slog` event.

### Unit of Work Concepts in `clog`

| Concept | `canonical-logger` (`clog`) Mechanism | Behavior |
| :--- | :--- | :--- |
| **Open Unit of Work** | `ctx := clog.NewContext(ctx)` | Attaches a fresh, thread-safe `*FieldBag` to `ctx`. |
| **Accumulate Context** | `clog.WithGroupFields(ctx, "orderService.checkout", ...)` | Enriches layer-owned namespaces in memory without premature emission. |
| **Outcome Boundary** | `clog.Run(ctx, "checkout-order", func(ctx) error)` (and `RunValue`, `HTTPHandler`) | Derives `<event>-success` (`err == nil`) or `<event>-failure` (`err != nil` or panic) from the return value. |
| **Failure Preservation** | `DefaultErrorFormatter` + `bag.Attrs()` | Populates the `"error"` group while keeping all prior `actor`, `request`, service, and DAO fields intact. |
| **Sticky Failure** | Internal outcome state on `FieldBag` | Once a failure or `"error"` group is recorded, a later `nil` return cannot downgrade the unit of work to `-success`. |
| **Nested Scope** | Nested `clog.Run` calls | Shares the same `FieldBag`; the outermost wrapper owns the event name. |
| **Fork Child Unit of Work** | `clog.Go(ctx, name, fn)` | Starts a fresh `FieldBag` over `context.WithoutCancel` and snapshots the parent bag into a `"parent"` group. |
| **Emit Wide Event** | `LoggerFromContext(ctx).LogAttrs(...)` | Flushes one canonical JSON log line when the middleware or task runner exits. |

### The 4-Phase Unit of Work Lifecycle

1. **Open (`clog.NewContext`)**:
   - Transport middleware (`HTTPMiddleware`, Gin/gRPC interceptors) or a boundary runner (`Run`, `RunFunc`, `Go`) initializes a fresh, mutex-guarded `*FieldBag` on `context.Context`.
   - If no `FieldBag` is present in `ctx` when `clog.Run` is called, `Run` opens one and flushes the canonical log line when `fn` returns. If no `FieldBag` is present when `clog.With*` is called directly, it is a safe no-op, so domain services remain decoupled from transport setup in unit tests.
2. **Accumulate (`clog.WithGroupFields`, `clog.WithGroupAttrs`, etc.)**:
   - Each architectural layer writes to its owned namespace (`actor`, `request`, `<service>.<method>`, `<dao>.<method>`).
   - Values are held in memory inside the `FieldBag` and converted to `[]slog.Attr` only once at emission time.
3. **Record Outcome (`clog.Run`, `clog.RunValue`, `clog.HTTPHandler`)**:
   - The boundary wrapper executes `fn(ctx)` and derives the outcome strictly from `fn`'s return value (see [ADR-0002](./adr/0002-return-value-result-flow.md)):
     - **`err == nil`**: Sets message to `<event>-success` (unless the unit of work was already marked failed by an earlier wrapper or an existing `"error"` group).
     - **`err != nil` or `panic`**: Marks the unit of work as failed, sets message to `<event>-failure`, populates the `"error"` group via `ErrorFormatter`, and re-panics if a panic occurred.
4. **Emit (`LoggerFromContext(ctx).LogAttrs(...)`)**:
   - When the outermost middleware or background wrapper unwinds, it inspects `bag.HasGroup("error")`, `bag.HasWarning()`, `bag.Message()`, and `bag.Attrs()`, and emits the entire unit of work as a single wide log event.

### Point Logging vs. Unit-of-Work Logging (`log/slog` vs. `clog`)

`clog` deliberately omits ad-hoc emission functions (`clog.Info`, `clog.Error`) so application layers cannot bypass the Unit of Work model:
- **Process lifecycle events** outside a Unit of Work (server startup, listening port, graceful shutdown signals) use standard `slog.Info` / `slog.Error` directly, which routes through the `clog.NewJSONHandler` or `clog.NewConsoleHandler` installed on `slog.SetDefault`.
- **One-off synchronous operations** (such as startup migrations) use `clog.Run(ctx, name, fn)` to open, record, and emit a self-contained Unit of Work.
- **In-flight diagnostics** inside a request or task are attached to the active `FieldBag` via `clog.WithGroupField(s)` rather than emitted as separate log lines.

---

## 2. Module Topology & Zero-Dependency Philosophy

`canonical-logger` is a **100% pure Go standard-library module** (`log/slog`, `context`, `net/http`). It never imports third-party web routers, OpenAPI generators, or cloud vendor SDKs.

```text
github.com/Shannor/canonical-logger (Pure Go Standard Library)
├── clog.go                 # Extended log levels, SamplingHandler, SampleEvaluator, NewJSONHandler, GCPReplaceAttr
├── console_handler.go      # Colorized human-readable ConsoleHandler for local development
├── fieldbag.go             # Thread-safe FieldBag, ContextResolver port, WithField(s)/WithAttrs helpers
├── result.go               # Run, RunValue, HTTPHandler (return-value results & standalone flush), ErrorFormatter
├── background.go           # Detached goroutines (Go) and zero-arg closures (RunFunc)
├── middleware.go           # Standard net/http HTTPMiddleware
└── trace.go                # HTTPTraceMiddleware & exported ExtractTrace port
```

---

## 3. Core Ports & How Userland Adapters Plug In

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

Boundary layers wrap their work in `clog.Run(ctx, "event-name", fn)` (or `RunValue`, `RunFunc`, `HTTPHandler`, `Go`, all built on `Run`). The outcome is derived from `fn`'s **returned error**, never from a captured variable (see ADR-0002). When the returned error is non-nil, `clog` invokes the configured `ErrorFormatter` (`DefaultErrorFormatter` by default) to populate the `"error"` group with `"err"`, unwrapped root `"cause"`, `"code"`, `"category"`, and `ErrorFields()`.
