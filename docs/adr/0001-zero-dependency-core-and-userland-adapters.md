# ADR-0001: Zero-Dependency Standard Library Core & Userland Adapter Recipes

- **Status:** Accepted
- **Date:** 2026-10-17

---

## Context & Problem Statement

`canonical-logger` (`clog`) was originally extracted from an internal service that used [Gin](https://github.com/gin-gonic/gin), [`oapi-codegen/gin-middleware`](https://github.com/oapi-codegen/gin-middleware), and the [Google Cloud Logging SDK (`cloud.google.com/go/logging`)](https://pkg.go.dev/cloud.google.com/go/logging).

Importing those third-party libraries directly in `canonical-logger` created two major issues:
1. **Transitive Dependency Bloat**: Every consumer of `clog`—even services using standard `net/http`, Chi, gRPC, or running on AWS/Kubernetes—was forced to pull in 60+ transitive dependencies (`gin`, `kin-openapi`, `cloud.google.com/go/logging`, `google.golang.org/grpc`, etc.).
2. **Combinatorial Adapter Coupling**: Even when attempting to isolate adapters into sub-modules (e.g. `adapter/gin`), framework ecosystems frequently overlap (for example, `gin` vs `oapi-codegen/gin-middleware`). Bundling `oapi-codegen` inside a Gin adapter still forces non-OpenAPI Gin users to pull in `oapi-codegen` and `kin-openapi`. Splitting every combination into separate sub-modules (`adapter/gin`, `adapter/gin-oapi`, `adapter/echo`, `adapter/fiber`, `adapter/grpc`, `adapter/gcp`, `adapter/aws`) creates an `N × M` maintenance and multi-module git-tagging burden for what is ultimately 10–35 lines of glue code per framework.

---

## Decision

We adopt a **100% Zero-Dependency Standard Library Core** that exposes composable **Ports**, paired with **Userland Adapter Recipes** in [`docs/adapters/`](../adapters/README.md):

### 1. Pure Go Standard Library Module (`github.com/Shannor/canonical-logger`)
- The repository contains a single root `go.mod` with **zero external runtime dependencies** (only Go's standard library: `log/slog`, `context`, `net/http`, `sync`, `time`).
- Pure `slog` handlers/helpers (`NewJSONHandler`, `NewConsoleHandler`, `NewSamplingHandler`, `GCPReplaceAttr`) and boundary outcome wrappers (`Run`, `RunValue`, `RunFunc`, `HTTPHandler`, `Go`) ship directly in the core package.
- Transport middlewares (`net/http`, Gin, gRPC) and vendor-specific trace extractors live in userland recipes under [`docs/adapters/`](../adapters/README.md).
- The legacy `pkg/xlog` and `pkg/clog` compatibility shims are removed.

### 2. Core "Ports" for Any Framework or Sink
Rather than importing third-party frameworks, `clog` exports clean Ports so any router, RPC interceptor, context wrapper, or cloud SDK writer can be wired in userland without accessing private package internals:
- **Pluggable Context Resolution (`ContextResolver` & `RegisterContextResolver`)**:
  - `BagFromContext(ctx)` checks the standard Go `context.Context` key first (O(1) fast path), and only if absent iterates registered `ContextResolver` hooks.
  - Applications using Gin or `oapi-codegen` register a 6-line `ContextResolver` in their own middleware package.
- **FieldBag Inspection (`HasGroup`, `HasWarning`, `Attrs`, `Message`)**:
  - Exported on `*FieldBag` so custom middlewares and interceptors can determine log severity escalation (`INFO` vs `WARN` vs `ERROR`) and emit accumulated attributes.
- **Pluggable Error Formatting (`ErrorFormatter` & `SetErrorFormatter`)**:
  - Allows customizing how domain errors are formatted into the `"error"` group at boundary layers.

### 3. Documented Adapter Recipes Instead of In-Repo Sub-Modules
- We do **not** maintain third-party adapter sub-modules (`adapter/gin`, `adapter/gcp`, etc.) in this repository.
- Instead, [`docs/adapters/`](../adapters/README.md) provides complete, copy-pasteable reference implementations in dedicated files for `net/http`, Gin, `oapi-codegen`, gRPC, distributed tracing, and the Google Cloud Logging SDK `io.Writer`.

---

## Alternatives Considered

1. **Monolithic `go.mod` with Framework Imports in Core**:
   - *Rejected*: Forces 60+ transitive dependencies onto every consumer.
2. **Multi-Module Monorepo (`adapter/gin/go.mod`, `adapter/gcp/go.mod`, `go.work`)**:
   - *Evaluated and Superseded*: While subdirectory `go.mod` files isolate dependencies, they require lockstep path-prefixed git tags (`adapter/gin/v0.3.0`), `go.work` management, and ongoing dependency maintenance for every framework combination (such as `gin` vs `oapi-codegen/gin-middleware`). Because `clog`'s Ports make custom middlewares and resolvers trivial (~10–35 lines), userland recipes are simpler and more flexible.

---

## Consequences

- **Positive:**
  - `go.mod` has **zero runtime dependencies**.
  - Single-module repository with simple semantic versioning (`vX.Y.Z`)—no `go.work` or multi-tag release scripts required.
  - Users have full control over their framework middleware and only import the exact libraries their service already uses (e.g. Gin without `oapi-codegen`, or Gin with `oapi-codegen`).
- **Trade-offs:**
  - Users copy a ~30-line middleware snippet from [`docs/adapters/`](../adapters/README.md) into their application's middleware package rather than importing a pre-packaged adapter module.
