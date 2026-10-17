# AGENTS.md — Canonical Logging Guide (`clog`)

This file provides architectural rules, conventions, and operational instructions for AI agents (and human engineers) implementing or interacting with `canonical-logger` (`clog`) in Go projects.

All code authored, edited, or reviewed by agents MUST adhere strictly to the guidelines defined in this document.

---

## 1. Core Architectural Pattern

### The Canonical Log Line Pattern

Rather than scattering multiple `log.Info()` or `log.Debug()` lines across handlers, services, and DAOs, `clog` implements the **Canonical Log Line** pattern:
- **One Rich Event Per Request/Task**: Exactly one structured JSON log line is emitted per HTTP request or background task lifecycle.
- **Context Accumulation**: Metadata is accumulated in a thread-safe `FieldBag` attached to `context.Context` as execution traverses the stack.
- **Emission at the Boundary**: The canonical line is emitted at the end of the request by `clog.HTTPMiddleware` or `cloggin.Middleware` (`adapter/gin`), or upon task exit by `clog.Go` / `clog.Func` / `clog.CronFunc`.
- **Zero Passing of Logger Instances**: Domain services and DAOs only need `context.Context` to enrich logs. Never pass logger instances through function signatures.
- **Ports & Adapters Module Boundary**: The root module (`github.com/Shannor/canonical-logger`) is a pure Go standard-library package. Domain services and DAOs MUST only import the root `clog` package. Framework and cloud SDK adapters (`adapter/gin`, `adapter/gcp`) are isolated sub-modules imported only at application entrypoints (`main.go` / server setup).

---

## 2. Layer Responsibilities & Group Ownership

Every layer in the application has strict boundaries regarding what it may and may not touch in the `FieldBag`:

| Layer | Functions Permitted | Group Name | Responsibilities |
| --- | --- | --- | --- |
| **Middleware** | `clog.HTTPMiddleware`, `cloggin.Middleware`, `clog.HTTPTraceMiddleware`, `cloggin.TraceID`, Auth middlewares | `actor` / root fields | Emits root HTTP metadata (`status`, `method`, `path`, `ip`, `latency`, `user-agent`, `traceId`, `spanId`) and identity context (`actor`). |
| **Boundary (API / Webhook Handlers)** | `SetResult()`, `DeferResult()`, `WithGroupFields()`, `WithGroupAttrs()` | `request` | Owns the event name (`-success` / `-failure`), captures request payload metadata and query filters (`request`). |
| **Service Layer** | `WithGroupFields()`, `WithGroupField()`, `WithGroupAttrs()` | `<serviceName>.<methodName>` | Records business execution details. MUST NOT set event names or populate the `"error"` group. Returns standard Go errors. |
| **DAO / DB Layer** | `WithGroupFields()`, `WithGroupField()`, `WithGroupAttrs()` | `<daoName>.<methodName>` or `db.<methodName>` | Records query metadata (`table`, `rowsReturned`, `durationMs`). MUST NOT set event names or populate `"error"`. Returns standard Go errors. |
| **Background / Cron** | `clog.Go()`, `clog.CronFunc()`, `clog.Func()`, `SetResult()` | `job` / `request` | Spawns isolated contexts with fresh FieldBags, correlates with parent snapshots, and handles panic recovery. |

---

## 3. Group & Field Naming Conventions

### Group Naming Rules

Groups are emitted as nested JSON objects in the canonical log line. Group names MUST follow these conventions:

- **Middleware**: Group MUST be `actor` (e.g. `requestorId`, `isAdmin`, `targetId`).
- **API Handler**: Group MUST be `request` (e.g. `campaignId`, `limit`, `offset`, `filters`).
- **Service Layer**: Group MUST follow `<serviceName>.<methodName>` (e.g. `campaignService.create`, `billingService.chargeCard`).
- **DAO / DB Layer**: Group MUST follow `<daoName>.<methodName>` or `db.<methodName>` (e.g. `campaignDAO.getByID`, `db.queryAttendees`).
- **Error Group**: Group MUST be `error`. Populated **automatically** by `SetResult` / `DeferResult` on failure.

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

## 4. Boundary Error Tracking & The Shadowing Gotcha

### Error Ownership Rules

- Inner layers (Service & DAO) MUST NOT manually populate the `"error"` group.
  - **FORBIDDEN**: `clog.WithGroupFields(ctx, "error", ...)` or `clog.WithGroupField(ctx, "error", "err", err.Error())`.
- If an inner layer needs to attach context to an error, it wraps it using standard Go formatting:
  ```go
  return fmt.Errorf("campaignService.Create: failed to persist campaign: %w", err)
  ```
- The `"error"` group is populated exclusively at boundary layers by calling `clog.SetResult(ctx, "event-name", err)` or `defer clog.DeferResult(ctx, "event-name", &err)`.

### Event Naming Convention

- Event names follow `resource-action` (e.g. `create-campaign`, `charge-card`, `sync-orders`).
- `SetResult` / `DeferResult` automatically appends `-success` or `-failure`:
  - `err == nil` -> `create-campaign-success`
  - `err != nil` -> `create-campaign-failure` (escalates log level to `ERROR` and sets `error.err`).

### The Error Shadowing Gotcha in `DeferResult`

When using `defer clog.DeferResult(ctx, "operation", &err)`, the deferred call captures the pointer to the function-level `err` variable. If you shadow `err` inside a block using `:=`, the outer `err` remains `nil`, causing the log to falsely report **success**!

```go
// ❌ WRONG: Shadowing err inside a block results in a FALSE SUCCESS log
func (api CampaignAPI) Create(c *gin.Context) {
    ctx := c.Request.Context()
    var err error
    defer clog.DeferResult(ctx, "create-campaign", &err)

    if c.Query("validate") == "true" {
        // := creates a NEW local err variable that shadows the outer err!
        res, err := api.validator.Check(ctx)
        if err != nil {
            c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
            return // Falsely logs "create-campaign-success" because outer err is still nil!
        }
    }
}

// ✅ CORRECT: Assign to the outer err variable
func (api CampaignAPI) Create(c *gin.Context) {
    ctx := c.Request.Context()
    var err error
    defer clog.DeferResult(ctx, "create-campaign", &err)

    if c.Query("validate") == "true" {
        var res ValidationResult
        res, err = api.validator.Check(ctx) // Reuses outer err
        if err != nil {
            c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
            return // Correctly logs "create-campaign-failure"
        }
    }
}
```

---

## 5. End-to-End Implementation Templates

### 1. HTTP API Handler (Boundary Layer)

```go
func (api CampaignAPI) CreateCampaign(c *gin.Context) {
    ctx := c.Request.Context()
    var err error
    defer clog.DeferResult(ctx, "create-campaign", &err)

    var req CreateCampaignRequest
    if err = c.ShouldBindJSON(&req); err != nil {
        c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
        return
    }

    // Enrich "request" group with payload metadata
    clog.WithGroupFields(ctx, "request", clog.Fields{
        "name":       req.Name,
        "recipients": len(req.RecipientIDs),
    })

    var campaign *models.Campaign
    campaign, err = api.campaignService.Create(ctx, req)
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
        return
    }

    c.JSON(http.StatusOK, campaign)
}
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
    ctx := c.Request.Context()
    campaignID := c.Param("id")

    // Spawns a detached goroutine with its own FieldBag, panic recovery,
    // and copies the parent context bag into a "parent" group.
    clog.Go(ctx, "send-campaign-bg", func(bgCtx context.Context) {
        var err error
        defer clog.DeferResult(bgCtx, "send-campaign-bg", &err)

        err = api.campaignService.Send(bgCtx, campaignID)
    })

    c.JSON(http.StatusAccepted, gin.H{"status": "queued"})
}
```

### 5. Scheduled Cron Jobs (`clog.CronFunc`)

```go
func RegisterCronJobs(c *cron.Cron, svc CampaignService) {
    c.AddFunc("@hourly", clog.CronFunc("sync-campaign-metrics-cron", func(ctx context.Context) {
        var err error
        defer clog.DeferResult(ctx, "sync-campaign-metrics-cron", &err)

        err = svc.SyncMetrics(ctx)
    }))
}
```

---

## 6. Pre-Commit & Review Checklist for Agents

Before completing any task that modifies or adds logging code, verify the following:

- [ ] **Boundary Layer Ownership**: Only API handlers, background tasks (`clog.Go`), and cron functions call `SetResult` or `DeferResult`.
- [ ] **No Error Grouping in Inner Layers**: Services and DAOs do NOT call `clog.WithGroupFields(ctx, "error", ...)`. They return wrapped Go errors.
- [ ] **Error Shadowing Prevented**: In functions using `defer clog.DeferResult(ctx, "...", &err)`, no nested block shadows `err` with `:=`.
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
