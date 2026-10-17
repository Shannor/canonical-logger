# ADR-0001: Ports & Adapters (Hexagonal) Multi-Module Architecture

- **Status:** Accepted
- **Date:** 2026-10-14

---

## Context & Problem Statement

`canonical-logger` (`clog`) was originally extracted from an internal service that used [Gin](https://github.com/gin-gonic/gin), [`oapi-codegen/gin-middleware`](https://github.com/oapi-codegen/gin-middleware), and the [Google Cloud Logging SDK (`cloud.google.com/go/logging`)](https://pkg.go.dev/cloud.google.com/go/logging).

Because those packages were imported directly in the root module (`github.com/Shannor/canonical-logger`), every consumer of `clog`—even services using standard `net/http`, Chi, gRPC, or running on AWS/Kubernetes—was forced to pull in over 60 transitive dependencies, including:
- `github.com/gin-gonic/gin` and its JSON/validation dependencies
- `github.com/getkin/kin-openapi`
- `cloud.google.com/go/logging`, `google.golang.org/api`, and `google.golang.org/grpc`

In Go, dependencies are resolved at the **module (`go.mod`) level**, not the individual package import level. Simply moving `GinMiddleware` or `GCPWriter` into a subpackage inside the same `go.mod` would still pollute every consumer's `go.mod` and `go.sum` with those external dependencies.

At the same time, we want to provide first-class, plug-and-play adapters for popular routers, protocols, and cloud providers (similar to how GORM provides `gorm.io/driver/postgres` or how `redis/go-redis` provides `extra/redisotel`) without maintaining separate Git repositories for every adapter.

---

## Decision

We adopt a **Ports & Adapters (Hexagonal Architecture)** combined with Go's **Multi-Module Repository** pattern (subdirectory `go.mod` files):

### 1. Zero-Dependency Standard Library Core (`github.com/Shannor/canonical-logger`)
- The root module depends **exclusively on the Go standard library** (`log/slog`, `context`, `net/http`, `sync`, `time`, etc.).
- Standard `net/http` middlewares (`HTTPMiddleware`, `HTTPTraceMiddleware`) and pure `slog` helpers (`NewJSONHandler`, `NewConsoleHandler`, `NewSamplingHandler`, `GCPReplaceAttr`) remain in the core module because they require zero third-party dependencies.
- The legacy `pkg/xlog` compatibility shim is removed in favor of a clean break.

### 2. Core "Ports" for External Adapters
To allow external adapters to integrate without accessing private package internals or requiring core to import third-party frameworks, `clog` exposes clean, pure-stdlib ports:
- **Pluggable Context Resolution (`ContextResolver` & `RegisterContextResolver`)**:
  - Previously, `BagFromContext(ctx)` hardcoded checks for `gin.ContextKey` and `ginmiddleware.GetGinContext(ctx)`.
  - Now, `BagFromContext(ctx)` checks the standard Go `context.Context` value first (O(1) fast path), and only if absent iterates registered `ContextResolver` functions.
  - Framework adapters (such as `adapter/gin`) register their resolver in `func init()` (mirroring the standard library `database/sql` driver registration pattern).
- **FieldBag Inspection (`HasGroup`, `HasWarning`)**:
  - Exported on `*FieldBag` so external middleware adapters can determine log severity escalation (`INFO` vs `WARN` vs `ERROR`).
- **Trace Extraction (`ExtractTrace`)**:
  - Exported so router and RPC adapters can reuse trace extraction and fallback ID generation.

### 3. Isolated Sub-Module Adapters (`adapter/<name>/go.mod`)
- Every integration that requires a third-party dependency lives under `adapter/<name>` with its own `go.mod`:
  - `github.com/Shannor/canonical-logger/adapter/gin` (`package cloggin`)
  - `github.com/Shannor/canonical-logger/adapter/gcp` (`package cloggcp`)
- In Go, a subdirectory containing a `go.mod` file is carved out of the parent module. Consumers who run `go get github.com/Shannor/canonical-logger` download **zero** third-party dependencies. Consumers who need Gin or the GCP SDK explicitly install `github.com/Shannor/canonical-logger/adapter/gin` or `adapter/gcp`.

### 4. Local Development via `go.work` & Lockstep Git Tagging
- **Local Development**: A workspace file (`go.work`, initialized via `make work-init` and excluded via `.gitignore`) links the root module and all `./adapter/*` modules locally so cross-module changes are tested immediately without `replace` directives in `go.mod`.
- **Lockstep Versioning**: Following the pattern used by `redis/go-redis` and `opentelemetry-go-contrib`, releases tag the root module (`vX.Y.Z`) and all adapter modules (`adapter/<name>/vX.Y.Z`) with matching version numbers via `make bump VERSION=vX.Y.Z` and `make tag VERSION=vX.Y.Z`.

---

## Alternatives Considered

1. **Single `go.mod` with Subpackages (`adapter/gin` without its own `go.mod`)**:
   - *Rejected*: Although Go only compiles imported packages into the final binary, Go's module graph still includes all dependencies listed in the root `go.mod`, forcing every consumer's `go.mod` to resolve Gin, OpenAPI, gRPC, and Google Cloud packages.
2. **Separate Git Repositories per Adapter (GORM style)**:
   - *Rejected for now*: Splitting into many small Git repositories adds significant overhead for CI/CD, cross-repo PRs, and issue tracking. A multi-module monorepo achieves the exact same module isolation for consumers while keeping all code, tests, and documentation in one repository.
3. **Removing All Third-Party Adapters Completely**:
   - *Rejected*: Providing official, tested plug-and-play adapters (`adapter/gin`, `adapter/gcp`, and future `adapter/grpc`, `adapter/otel`) significantly lowers adoption friction for teams using those frameworks.

---

## Consequences

- **Positive:**
  - The root `go.mod` drops from 70+ direct and indirect dependencies to **zero runtime dependencies**.
  - Domain services and DAOs only ever import `github.com/Shannor/canonical-logger` (`clog`). Only `main.go` / server wiring imports `adapter/gin` or `adapter/gcp`.
  - New adapters (`adapter/grpc`, `adapter/otel`, `adapter/echo`, `adapter/fiber`) can be added in the future with zero impact on the core package or other adapters.
- **Trade-offs / Operational Notes:**
  - Releasing a new version requires tagging both the root module (`vX.Y.Z`) and each adapter module (`adapter/<name>/vX.Y.Z`). This is automated via `make bump`, `make tag`, and `make push-tags` in the root `Makefile`.
