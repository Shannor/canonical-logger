# Architecture Documentation & Decision Records (`docs/`)

This directory contains architectural documentation and **Architecture Decision Records (ADRs)** for `canonical-logger` (`clog`).

ADRs capture significant architectural decisions, the context and constraints behind them, the alternatives evaluated, and their long-term consequences.

---

## Architecture Guides

- **[Architecture Guide (`docs/architecture.md`)](./architecture.md)**: Module topology, dependency rules, Core Ports (`ContextResolver`, `ErrorFormatter`, `FieldBag` inspection), `go.work` local development, and lockstep release versioning.

---

## Architecture Decision Records

| ID | Title | Status | Date |
| :--- | :--- | :--- | :--- |
| [ADR-0001](./adr/0001-ports-and-adapters-multi-module-architecture.md) | Ports & Adapters (Hexagonal) Multi-Module Architecture | Accepted | 2026-10-14 |

---

## Related Documentation

- **[Root `README.md`](../README.md)**: Getting started, layer conventions, and API usage guide.
- **[`adapter/README.md`](../adapter/README.md)**: Guide to `adapter/*` sub-modules, `go.work` local development, and lockstep Git tagging.
- **[`AGENTS.md`](../AGENTS.md)**: Rules and conventions for AI agents and engineers writing canonical logging code.
- **[`TODO.md`](../TODO.md)**: Project roadmap and upcoming architectural milestones.
