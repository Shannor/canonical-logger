# `canonical-logger` Adapters

This directory contains framework, protocol, and cloud vendor adapters for `canonical-logger` (`clog`).

Each adapter lives in its own **isolated Go module** (containing its own `go.mod`) so that the root module (`github.com/Shannor/canonical-logger`) remains a **100% pure Go standard library** package with zero external runtime dependencies.

For the full architectural rationale behind this design, see [ADR-0001: Ports & Adapters Multi-Module Architecture](../docs/adr/0001-ports-and-adapters-multi-module-architecture.md).

---

## Available Adapters

| Adapter Module | Package Name | Description |
| :--- | :--- | :--- |
| [`github.com/Shannor/canonical-logger/adapter/gin`](./gin) | `cloggin` | Gin HTTP middleware (`Middleware`), trace propagation (`TraceID`), and automatic `gin.Context` / `oapi-codegen` `ContextResolver` registration. |
| [`github.com/Shannor/canonical-logger/adapter/gcp`](./gcp) | `cloggcp` | Google Cloud Logging SDK `io.Writer` (`Writer` / `GCPWriter`) and `ReplaceAttr` helper. |

---

## How Local Development Works (`go.work`)

Because each adapter has its own `go.mod` referencing `github.com/Shannor/canonical-logger`, Go needs to know during local development that changes in the root directory should be visible immediately to `./adapter/*` without publishing a new Git tag first.

We use **Go Workspaces (`go.work`)** for this:

1. **Initialize the workspace** (only needed once after cloning or adding a new adapter):
   ```bash
   make work-init
   ```
   This creates a local `go.work` file at the repository root:
   ```go
   go 1.26.4

   use (
       .
       ./adapter/gcp
       ./adapter/gin
   )
   ```

2. **How `go.work` operates**:
   - When `go.work` is present in the root directory, any `go test`, `go build`, or IDE language server (`gopls`) command run anywhere in the repository resolves `github.com/Shannor/canonical-logger` directly to your local checkout (`.`) instead of downloading a published version from `proxy.golang.org`.
   - You never need temporary `replace => ../..` directives inside `adapter/*/go.mod`.
   - Per official Go guidelines, `go.work` and `go.work.sum` are ignored in `.gitignore` so each module's `go.mod` stays clean for downstream consumers.

3. **Running tests across all modules**:
   ```bash
   make test
   ```

---

## How Core Ports Connect to Adapters

The root `clog` package exposes pure-stdlib **Ports** that adapters use to integrate seamlessly:

1. **`clog.RegisterContextResolver(resolver clog.ContextResolver)`**:
   - Frameworks like Gin (or OpenAPI code generators) sometimes wrap or replace `c.Request` with a new context that only stores `*gin.Context` under a framework-specific key.
   - An adapter registers its `ContextResolver` in `func init()` so `clog.BagFromContext(ctx)` can fall back to extracting the `*clog.FieldBag` from the framework context without `clog` ever importing that framework.
2. **`(*clog.FieldBag).HasGroup(name)` & `(*clog.FieldBag).HasWarning()`**:
   - Allows external middleware adapters to inspect accumulated fields and determine log severity escalation (`INFO` vs `WARN` vs `ERROR`).
3. **`clog.ExtractTrace(sentryTrace, baggage)`**:
   - Allows protocol adapters (HTTP, Gin, gRPC) to reuse core trace extraction and fallback ID generation.

---

## Adding a New Adapter

To create a new adapter (e.g., `adapter/grpc` or `adapter/echo`):

1. Create the directory and initialize its module:
   ```bash
   mkdir -p adapter/grpc
   cd adapter/grpc
   go mod init github.com/Shannor/canonical-logger/adapter/grpc
   ```
2. Add the new adapter to your local `go.work` workspace:
   ```bash
   make work-init
   ```
3. Implement your adapter and tests, then run:
   ```bash
   make tidy
   make test
   ```
   Because the root `Makefile` dynamically discovers `adapter/*/go.mod`, your new adapter will automatically be included in `make test`, `make tidy`, `make bump`, and `make tag`.

---

## Versioning & Git Tagging

In Go multi-module repositories, subdirectory modules use path-prefixed Git tags (e.g., `adapter/gin/v0.3.0`) while the root module uses `v0.3.0`.

We use **lockstep versioning** via the root `Makefile` so all adapters share the same version number as core:

```bash
# 1. Bump the required canonical-logger version in all adapter/*/go.mod files
make bump VERSION=v0.3.0
git commit -am "chore: release v0.3.0"

# 2. Create tags: v0.3.0, adapter/gin/v0.3.0, adapter/gcp/v0.3.0, ...
make tag VERSION=v0.3.0

# 3. Push all tags to origin
make push-tags VERSION=v0.3.0
```
