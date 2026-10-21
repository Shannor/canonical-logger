# AGENTS.md — Canonical Logging Guide (`clog`)

This file provides architectural rules, conventions, and operational instructions for AI agents (and human engineers) implementing or interacting with `canonical-logger` (`clog`) in Go projects.

All code authored, edited, or reviewed by agents MUST adhere strictly to the guidelines defined in this document.

---

## 1. Core Architectural Pattern

### The Canonical Log Line / Wide Event per Unit of Work

Rather than scattering multiple `log.Info()` or `log.Debug()` side effects across handlers, services, and DAOs, `clog` implements the **Canonical Log Line / Wide Event** pattern scoped to a **Unit of Work**:
- **One Wide Event Per Unit of Work**: Exactly one structured JSON log line is emitted per HTTP request, RPC call, consumed message, or background task lifecycle.
- **Context Accumulation (`FieldBag`)**: Transport middleware or a task runner opens a thread-safe `FieldBag` (the unit of work) attached to `context.Context`. Metadata is accumulated in the `FieldBag` as execution traverses the stack.
- **Return-Value Outcome Boundary (`clog.Run`)**: Boundary wrappers (`HTTPHandler`, `Run`, `RunValue`, `RunFunc`, `Go`) capture the unit of work's outcome from the returned `error`: `nil` records `<event>-success`, while a non-nil error (or panic) records `<event>-failure` with sticky failure semantics, preserving all fields accumulated before the error.
- **Emission at the Boundary**: The canonical wide event is emitted once at the end of the request by `clog.HTTPMiddleware` (or a custom router middleware built on `clog`'s Ports—see `docs/adapters.md`), or automatically upon exit by `clog.Run` (when no outer middleware opened a `FieldBag`), `clog.RunFunc`, and `clog.Go`.
- **Zero Passing of Logger Instances**: Domain services and DAOs only need `context.Context` to enrich logs. Never pass logger instances through function signatures.
- **100% Zero-Dependency Core**: `github.com/Shannor/canonical-logger` is a pure Go standard-library package (`log/slog`, `context`, `net/http`). Third-party router middlewares (Gin, Echo, gRPC), `ContextResolver` hooks (`oapi-codegen`), and custom SDK writers (`GCPWriter`) live in userland using the Ports documented in `docs/adapters.md`.

### When to Use `clog` vs. Standard `log/slog`

`clog` intentionally provides no `clog.Info()` or `clog.Error()` point-logging functions so inner layers cannot scatter ad-hoc log lines during a request:
1. **Process Lifecycle Events (No Unit of Work)**: For startup/shutdown logs in `main()` (e.g. `"server listening on :8080"`, `"SIGTERM received"`), call standard `slog.Info()` / `slog.Error()` directly. They automatically use the `clog.NewJSONHandler` or `clog.NewConsoleHandler` configured on `slog.SetDefault`.
2. **One-Off Synchronous Tasks**: For standalone operations like startup migrations or CLI commands, wrap them in `clog.Run(ctx, "startup-migrations", func(ctx context.Context) error { ... })` to open a `FieldBag` and automatically flush a single wide event with latency and outcome on return.
3. **Inside an Active Request or Background Task**: **NEVER** call `slog.Info()` / `slog.Warn()` / `slog.Error()` inside handlers, services, or DAOs. Always attach fields to the active `FieldBag` via `clog.WithGroupFields()` / `clog.WithGroupField()`.

---

## 2. Layer Responsibilities & Group Ownership

Every layer in the application has strict boundaries regarding what it may and may not touch in the `FieldBag`:

| Layer | Functions Permitted | Group Name | Responsibilities |
| --- | --- | --- | --- |
| **Middleware** | `clog.HTTPMiddleware`, `clog.HTTPTraceMiddleware`, Custom router/Auth middlewares | `actor` / root fields | Emits root HTTP metadata (`status`, `method`, `path`, `ip`, `latency`, `user-agent`, `traceId`, `spanId`) and identity context (`actor`). |
| **Boundary (API / Webhook Handlers)** | `HTTPHandler()`, `Run()`, `RunValue()`, `WithGroupFields()`, `WithGroupAttrs()` | `request` | Owns the event name (`-success` / `-failure`), captures request payload metadata and query filters (`request`). |
| **Service Layer** | `WithGroupFields()`, `WithGroupField()`, `WithGroupAttrs()` | `<serviceName>.<methodName>` | Records business execution details. MUST NOT set event names or populate the `"error"` group. Returns standard Go errors. |
| **DAO / DB Layer** | `WithGroupFields()`, `WithGroupField()`, `WithGroupAttrs()` | `<daoName>.<methodName>` or `db.<methodName>` | Records query metadata (`table`, `rowsReturned`, `durationMs`). MUST NOT set event names or populate `"error"`. Returns standard Go errors. |
| **Background / Cron** | `clog.Go()`, `clog.RunFunc()`, `clog.Run()` | `job` / `request` | Spawns isolated contexts with fresh FieldBags, correlates with parent snapshots, and handles panic recovery. |

---

## 3. Group & Field Naming Conventions

### Group Naming Rules

Groups are emitted as nested JSON objects in the canonical log line. Group names MUST follow these conventions:

- **Middleware**: Group MUST be `actor` (e.g. `requestorId`, `isAdmin`, `targetId`).
- **API Handler**: Group MUST be `request` (e.g. `campaignId`, `limit`, `offset`, `filters`).
- **Service Layer**: Group MUST follow `<serviceName>.<methodName>` (e.g. `campaignService.create`, `billingService.chargeCard`).
- **DAO / DB Layer**: Group MUST follow `<daoName>.<methodName>` or `db.<methodName>` (e.g. `campaignDAO.getByID`, `db.queryAttendees`).
- **Error Group**: Group MUST be `error`. Populated **automatically** when a `Run`-based wrapper returns an error.

### Field Naming & Formatting Rules

- **Strict camelCase**: All map keys in `clog.Fields` MUST be **camelCase** (e.g. `"userId"`, `"campaignId"`, `"rowsReturned"`, `"durationMs"`).
- **The `Id` Suffix Convention**: Use `Id` (not `ID`) in JSON string keys so structured log searches (e.g. GCP Cloud Logging `jsonPayload.<group>.<field>`, Datadog) are uniform across all entities.
  - Go variable names continue to follow standard Go idiom (e.g. `userID`, `campaignID`).
  - The map key in `clog.Fields` MUST use `Id`:
    ```go
    // CORRECT
    clog.WithGroupFields(ctx, "campaignService.get", clog.Fields{
        "campaignId": campaignID,
        "userId":     userID,
    })

    // FORBIDDEN (snake_case or PascalCase)
    "campaign_id": campaignID,
    "campaignID":  campaignID,
    "CampaignId":  campaignID,
    ```
- **Always Use `clog.Fields`**: Never use bare `map[string]any` or `map[any]any` when calling `clog.WithGroupFields()` or `clog.WithFields()`.
- **Zero PII Policy**: **NEVER log PII** (Personally Identifiable Information). This includes phone numbers, email addresses, full names, or physical addresses. Always use entity IDs (UUIDs, DB keys).

---

## 4. Boundary Error Tracking (Return-Value Results)

### Error Ownership Rules

- Inner layers (Service & DAO) MUST NOT manually populate the `"error"` group.
  - **FORBIDDEN**: `clog.WithGroupFields(ctx, "error", ...)` or `clog.WithGroupField(ctx, "error", "err", err.Error())`.
- If an inner layer needs to attach context to an error, it wraps it using standard Go formatting:
  ```go
  return fmt.Errorf("campaignService.Create: failed to persist campaign: %w", err)
  ```
- Outcomes are recorded **only** from a function's return value, using `clog.Run` or a wrapper built on it:

  | Boundary | API |
  | --- | --- |
  | `net/http` handler | `clog.HTTPHandler(event, func(w, r) error)` |
  | Framework handler (Gin, Echo), webhook, consumer, sync task | `clog.Run(ctx, event, func(ctx) error) error` |
  | RPC handler returning a value | `clog.RunValue(ctx, event, func(ctx) (T, error)) (T, error)` |
  | Goroutine / zero-arg closure (cron, callbacks) | `clog.Go`, `clog.RunFunc` (both take `func(ctx) error`) |

- There is no `SetResult` / `DeferResult`. Never emulate them (e.g. via `WithMessage(ctx, "x-failure")` or a captured `err` variable).
- Failure is **sticky**: a `nil` result never downgrades an earlier failure or an existing `"error"` group.
- When wrappers are nested, the **outermost** wrapper owns the event name. Use exactly one wrapper per boundary.
- Panics inside a wrapper are recorded as `<event>-failure` and re-panicked.

### Event Naming Convention

- Event names follow `resource-action` (e.g. `create-campaign`, `charge-card`, `sync-orders`).
- The wrapper automatically appends `-success` or `-failure`:
  - returned `nil` -> `create-campaign-success`
  - returned error -> `create-campaign-failure` (escalates log level to `ERROR` and sets `error.err`).
- For `Go` / `RunFunc` / standalone `Run` the task name is the event name.

### Why There Is No `DeferResult` (The Shadowing Gotcha)

The old `var err error; defer clog.DeferResult(ctx, "x", &err)` pattern read the outcome from a captured variable. A nested `x, err := ...` declared a *new* `err`, the outer one stayed `nil`, and the line falsely logged **success**. `go vet` and the default `shadow` analyzer do not detect this (see `docs/adr/0002-return-value-result-flow.md`). Return-value wrappers make shadowing harmless:

```go
// ✅ := shadowing cannot affect the outcome: the returned error is the result
func (api CampaignAPI) Create(c *gin.Context) {
    _ = clog.Run(c.Request.Context(), "create-campaign", func(ctx context.Context) error {
        if c.Query("validate") == "true" {
            res, err := api.validator.Check(ctx)
            if err != nil {
                c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
                return err // -> create-campaign-failure
            }
            _ = res
        }
        return nil
    })
}
```

---

## 5. End-to-End Implementation Templates

### 1. HTTP API Handler (Boundary Layer)

```go
func (api CampaignAPI) CreateCampaign(c *gin.Context) {
    _ = clog.Run(c.Request.Context(), "create-campaign", func(ctx context.Context) error {
        var req CreateCampaignRequest
        if err := c.ShouldBindJSON(&req); err != nil {
            c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
            return err
        }

        // Enrich "request" group with payload metadata
        clog.WithGroupFields(ctx, "request", clog.Fields{
            "name":       req.Name,
            "recipients": len(req.RecipientIDs),
        })

        campaign, err := api.campaignService.Create(ctx, req)
        if err != nil {
            c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
            return err
        }

        c.JSON(http.StatusOK, campaign)
        return nil
    })
}

// net/http equivalent:
// mux.Handle("POST /campaigns", clog.HTTPHandler("create-campaign", api.CreateCampaign))
// where CreateCampaign has signature func(w http.ResponseWriter, r *http.Request) error.
```

### 2. Service Layer (Business Execution)

```go
func (s campaignService) Create(ctx context.Context, req CreateCampaignRequest) (*models.Campaign, error) {
    campaign := &models.Campaign{
        Name: req.Name,
    }

    err := s.dao.Insert(ctx, campaign)
    if err != nil {
        return nil, fmt.Errorf("campaignService.Create: %w", err)
    }

    // Enrich "<serviceName>.<methodName>" group
    clog.WithGroupFields(ctx, "campaignService.create", clog.Fields{
        "campaignId": campaign.ID,
        "status":     campaign.Status,
    })

    return campaign, nil
}
```

### 3. DAO Layer (Database / Storage Execution)

```go
func (d campaignDAO) Insert(ctx context.Context, c *models.Campaign) error {
    start := time.Now()
    err := d.db.WithContext(ctx).Create(c).Error

    // Enrich "<daoName>.<methodName>" group
    clog.WithGroupFields(ctx, "campaignDAO.insert", clog.Fields{
        "table":      "campaigns",
        "durationMs": time.Since(start).Milliseconds(),
    })

    if err != nil {
        return fmt.Errorf("campaignDAO.Insert: %w", err)
    }
    return nil
}
```

### 4. Background Goroutines (`clog.Go`)

When spawning asynchronous background tasks from an HTTP request or worker:

```go
func (api CampaignAPI) SendAsync(c *gin.Context) {
    _ = clog.Run(c.Request.Context(), "queue-campaign-send", func(ctx context.Context) error {
        campaignID := c.Param("id")
        clog.WithGroupField(ctx, "request", "campaignId", campaignID)

        // Spawns a detached goroutine with its own FieldBag and canonical line,
        // panic recovery, and a copy of the parent bag in a "parent" group.
        // The returned error becomes send-campaign-bg-success / -failure.
        clog.Go(ctx, "send-campaign-bg", func(bgCtx context.Context) error {
            return api.campaignService.Send(bgCtx, campaignID)
        })

        c.JSON(http.StatusAccepted, gin.H{"status": "queued"})
        return nil
    })
}
```

### 5. Scheduled Cron Jobs / Callbacks (`clog.RunFunc`)

```go
func RegisterCronJobs(c *cron.Cron, svc CampaignService) {
    c.AddFunc("@hourly", clog.RunFunc("sync-campaign-metrics-cron", svc.SyncMetrics))
}
```

---

## 6. Pre-Commit & Review Checklist for Agents

Before completing any task that modifies or adds logging code, verify the following:

- [ ] **Boundary Layer Ownership**: Only API handlers, background tasks, and cron functions record results, via exactly one wrapper (`HTTPHandler`, `Run`, `RunValue`, `RunFunc`, `Go`).
- [ ] **Return-Value Results**: Every error path inside a wrapper `return`s the error. No error path writes a failure response and then returns `nil`.
- [ ] **No Ad-Hoc `slog` Calls in Request/Task Paths**: Handlers, services, and DAOs do NOT call `slog.Info`, `slog.Warn`, or `slog.Error` during a Unit of Work; `slog.*` is reserved for process lifecycle events (`main()` startup/shutdown).
- [ ] **No Error Grouping in Inner Layers**: Services and DAOs do NOT call `clog.WithGroupFields(ctx, "error", ...)`. They return wrapped Go errors.
- [ ] **Group Naming Rules**:
  - API layer uses `"request"`.
  - Service methods use `"<serviceName>.<methodName>"`.
  - DAO methods use `"<daoName>.<methodName>"` or `"db.<methodName>"`.
- [ ] **Map Key Formatting**:
  - All keys are `camelCase`.
  - Entity IDs use the `Id` suffix string (e.g. `"campaignId"`, `"userId"`).
  - No `snake_case` or `PascalCase` keys.
- [ ] **Type Safety**: Field maps use `clog.Fields{...}`, never `map[string]any`.
- [ ] **Zero PII**: No emails, phone numbers, or passwords are logged. Only entity identifiers.
