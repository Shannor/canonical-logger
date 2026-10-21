# Architecture Documentation & Decision Records (`docs/`)

This directory contains architectural documentation, adapter authoring guides, and **Architecture Decision Records (ADRs)** for `canonical-logger` (`clog`).

---

## Guides

- **[Architecture Guide (`docs/architecture.md`)](./architecture.md)**: The **Canonical Log Line / Wide Event per Unit of Work** lifecycle, module topology, zero-dependency philosophy, and Core Ports (`ContextResolver`, `ErrorFormatter`, `FieldBag` inspection, `ExtractTrace`).
- **[Building Custom Adapters, Middlewares & Writers (`docs/adapters.md`)](./adapters.md)**: How to write custom router middlewares (Gin, Echo), `ContextResolver` hooks (`gin`, `oapi-codegen`), gRPC interceptors, and cloud SDK `io.Writer` sinks (GCP Cloud Logging).

---

## Architecture Decision Records

| ID | Title | Status | Date |
| :--- | :--- | :--- | :--- |
| [ADR-0001](./adr/0001-zero-dependency-core-and-userland-adapters.md) | Zero-Dependency Standard Library Core & Userland Adapter Recipes | Accepted | 2026-10-17 |
| [ADR-0002](./adr/0002-return-value-result-flow.md) | Return-Value Result Flow & Unit-of-Work Semantics (Remove `SetResult` / `DeferResult`) | Accepted | 2026-10-17 |

---

## Related Documentation

- **[Root `README.md`](../README.md)**: Getting started, layer conventions, and API usage guide.
- **[`AGENTS.md`](../AGENTS.md)**: Rules and conventions for AI agents and engineers writing canonical logging code.
- **[`TODO.md`](../TODO.md)**: Project roadmap and upcoming architectural milestones.
