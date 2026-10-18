# ADR-0002: Return-Value Result Flow (Remove `SetResult` / `DeferResult`)

- **Status:** Accepted
- **Date:** 2026-10-17
- **Breaking:** Yes (pre-v1; next release is `v0.4.0`)

---

## Context & Problem Statement

Boundary code used to record its outcome with:

```go
var err error
defer clog.DeferResult(ctx, "handle-action", &err)
```

`DeferResult` read `*err` when the function returned. If a nested block (`if`, `for`, `switch`, `select`) redeclared `err` with `:=`, the nested `err` was a **different variable**. The deferred pointer still pointed at the outer `err`, which stayed `nil`, so the canonical line logged **`handle-action-success` for a failed request**. A logging library must never let its own API produce a confident false negative.

The root cause isn't developer carelessness. **The outcome was read from a side-channel variable instead of the function's control flow.** Any API that observes the error through a captured variable (`&err`, `SetResult(ctx, e, err)` called from the right place by convention) has this problem.

### Research findings (verified against Go 1.27, x/tools v0.52.0)

| Mitigation | Catches the bug? | Notes |
| --- | --- | --- |
| `go vet` (default analyzers) | ❌ No | Clean on the buggy example. |
| `shadow` analyzer (default mode, also golangci-lint `govet: shadow`) | ❌ **No** | Non-strict mode only reports a shadow if the outer var is *mentioned after* the inner declaration. The `defer ... &err` mention comes **before**, so the bug is invisible. |
| `shadow -strict` | ✅ Yes | Also flags safe code and every idiomatic `err` shadow → too noisy to enforce. |
| `staticcheck`, `ineffassign`, `errcheck` | ❌ No | The inner `err` is used (`if err != nil`), so nothing is "unused". |
| Named result `func f() (err error)` + `DeferResult(&err)` | ✅ Yes (compiler) | `return innerErr` assigns the named result before defers run; a bare `return` while shadowed is a compile error. **But** it only works for functions that return `error` (not `http.HandlerFunc` / `gin.HandlerFunc`), and relies on a convention nothing enforces. |
| Transport reconciliation in middleware (status ≥ 500 ⇒ failure) | ⚠️ Partial | Misses shadowed errors with 2xx/4xx responses. Couples result correctness to an emitter, and HTTP middleware is slated to leave core (see below). **Rejected.** |
| **Error as return value** (`Run`, appHandler pattern) | ✅ Yes (structural) | The outcome *is* the return value, so shadowing can't affect it. Prior art: Go blog [*Error handling and Go*](https://go.dev/blog/error-handling-and-go) `appHandler`, Echo / go-kit handlers, `errgroup.Go(func() error)`, cobra `RunE`. |
| Go language change to `:=` semantics | — | Long-standing proposals (e.g. golang/go#377). Not going to land, so we can't rely on it. |

**Conclusion:** no static tool we can ship in a zero-dependency module catches the bug without heavy noise, and conventions only help in a subset of functions. The fix has to be in the API shape: **make the wrong thing unrepresentable.**

---

## Decision

### 1. Outcomes come only from return values

`Run` is the single public primitive for recording a boundary outcome. Everything else is built on it:

| API | Use for |
| --- | --- |
| `clog.Run(ctx, event, func(ctx) error) error` | Any boundary: framework handlers (Gin, Echo), webhooks, consumers. Returns the error to the caller. |
| `clog.RunValue[T](ctx, event, func(ctx) (T, error)) (T, error)` | RPC / gRPC handlers: `return clog.RunValue(ctx, "get-user", ...)`. |
| `clog.HTTPHandler(event, clog.HTTPHandlerFunc) http.Handler` | `net/http` / ServeMux / Chi (appHandler pattern). The handler still writes its own response. |
| `clog.Go(ctx, name, func(ctx) error)` | Detached goroutine. `name` is the event name. |
| `clog.Func(name, func(ctx) error) error` | Synchronous task (startup, migrations). Returns the error. |
| `clog.CronFunc(name, func(ctx) error) func()` | Cron job body. |

### 2. `SetResult` and `DeferResult` are removed

The logic lives on as the unexported `recordResult`. There is no public way to set an outcome other than returning an error from a wrapper. (`WithMessage` still exists for custom messages but carries no outcome semantics.)

`Go` / `Func` / `CronFunc` change signature from `func(ctx)` to `func(ctx) error` rather than gaining `...E` variants. A single way to do it is simpler to teach and lint, and pre-v1 is the right time to break.

### 3. Semantics

- `nil` → `<event>-success`. Error → `<event>-failure`, `"error"` group populated via `ErrorFormatter`, `ERROR` level.
- **Panics** inside `Run` are recorded as `<event>-failure` and **re-panicked**. `Go` still recovers them. `Func` / `CronFunc` flush the canonical line and let the panic propagate.
- **Sticky failure**: the bag tracks an outcome state. A `nil` result never downgrades an earlier failure (e.g. a nested `Run`) or a bag that already carries an `"error"` group (e.g. attached by a router adapter).
- **Nesting**: the outermost wrapper owns the event name (it records last).
- Background tasks now always emit `<name>-success` / `<name>-failure` instead of the generic `canonical-log-line`.

### 4. Result correctness does not depend on middleware

An earlier draft reconciled `-success` + HTTP 5xx in `HTTPMiddleware`. It was dropped:
- it only covers HTTP and only 5xx;
- it ties correctness to an emitter;
- `HTTPMiddleware` / `HTTPTraceMiddleware` are planned to move out of core into copy-pasteable recipes in `docs/adapters.md`, like the Gin / Echo / gRPC recipes (TODO §1).

Emitters (core or userland) only *read* the bag (`Message`, `HasGroup`, `HasWarning`, `Attrs`) and never record or rewrite outcomes.

### 5. Core scope rule: record in core, emit in recipes

Core may contain **standard-library-only helpers that record outcomes** (`Run`, `RunValue`, `HTTPHandler`, `Go`, `Func`, `CronFunc`). Helpers that **emit** canonical lines or **reconcile/rewrite** outcomes from transport state belong in userland recipes (`docs/adapters.md`), alongside the Gin / Echo / gRPC recipes.

Applying the rule:
- `clog.HTTPHandler` / `clog.HTTPHandlerFunc` **stay in core**. They are ~6 lines over `net/http`, never emit, and make the shadow-proof pattern the shortest one for every `http.Handler`-compatible router (ServeMux, Chi, gorilla). They are a no-op when no bag is on the request context, so they don't depend on `HTTPMiddleware`.
- They use the `HTTP` prefix (not `Handler` / `HandlerFunc`) because in this package "Handler" already means `slog.Handler` (`SamplingHandler`, `ConsoleHandler`, `NewJSONHandler`). The prefix also matches `HTTPMiddleware` / `HTTPTraceMiddleware`.
- `HTTPHandler` deliberately does **not** render error responses. Response rendering is application policy.
- Framework equivalents (e.g. a `gin.HandlerFunc` adapter) are not added to core. They live in recipes per ADR-0001.

---

## Migration

| Before | After |
| --- | --- |
| `var err error; defer clog.DeferResult(ctx, "e", &err)` in `http.HandlerFunc` | `clog.HTTPHandler("e", func(w, r) error { ... return err })` (handler type: `clog.HTTPHandlerFunc`) |
| Same, in a Gin/Echo handler | `_ = clog.Run(c.Request.Context(), "e", func(ctx context.Context) error { ... })` |
| `func (...) (_ *T, err error) { defer clog.DeferResult(ctx, "e", &err) ... }` | `return clog.RunValue(ctx, "e", func(ctx context.Context) (*T, error) { ... })` |
| `clog.SetResult(ctx, "e", err)` | Return `err` from the enclosing wrapper. |
| `clog.Go(ctx, "n", func(ctx) { var err error; defer clog.DeferResult(ctx, "n", &err); err = do(ctx) })` | `clog.Go(ctx, "n", do)` |
| `clog.Func("n", func(ctx) {...})` / `clog.CronFunc("n", func(ctx) {...})` | Same names, `fn` now returns `error`. |

---

## Consequences

### Positive

- The shadowing bug can't be expressed with the public API.
- One way to record an outcome → simpler docs, reviews and agent rules.
- No new dependencies. `Run` success path is ~21 ns / 1 alloc (same allocation profile as the old `SetResult`).
- The bag's structured outcome state is a stepping stone toward TODO §5 `OutcomeStrategy`.

### Negative / Trade-offs

- Breaking change for all `SetResult` / `DeferResult` / `Go` / `Func` / `CronFunc` callers.
- One extra closure level in framework handlers (`clog.Run(ctx, "e", func(ctx) error {...})`).
- `clog.HTTPHandler` doesn't write an error response. Returning an error without writing still sends HTTP 200.
- A handler can still write a 500 and `return nil`. That's an explicit, reviewable mistake rather than an invisible one, and could be flagged by a future analyzer.

### Follow-ups

- Move `HTTPMiddleware` / `HTTPTraceMiddleware` to `docs/adapters.md` recipes (TODO §1).
- Optional `clogvet` analyzer in a **separate module** (keeps core zero-dependency): flag error paths inside `Run`/`HTTPHandler` closures that write an error response and then `return nil`.
