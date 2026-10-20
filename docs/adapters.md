# Building Custom Adapters, Middlewares & Writers

`canonical-logger` (`clog`) intentionally ships as a **100% pure Go standard-library package** (`log/slog`, `context`, `net/http`) with zero external runtime dependencies.

Instead of bundling and maintaining dozens of third-party router, RPC, OpenAPI, and cloud SDK subpackages, `clog` exposes small, composable **Ports** in the root package. You can wire any framework or log sink into `clog` in ~20–40 lines of code inside your own application.

---

## 1. The Core Ports in `clog`

Every canonical logging integration uses four building blocks exported by `github.com/Shannor/canonical-logger`:

| Port / Function | Purpose |
| :--- | :--- |
| `clog.NewContext(ctx)` | Opens a new Unit of Work by creating a fresh `*clog.FieldBag` (inheriting any context `*slog.Logger`) and returning the enriched `context.Context`. |
| `clog.BagFromContext(ctx)` | Retrieves the active `*clog.FieldBag` from `ctx` (or via registered `ContextResolver` hooks). |
| `clog.RegisterContextResolver(fn)` | Registers a fallback `func(ctx context.Context) *clog.FieldBag` for frameworks that wrap or replace `context.Context`. |
| `bag.HasGroup("error")` / `bag.HasWarning()` / `bag.Message()` / `bag.Attrs()` | Inspects the finalized Unit of Work at the end of a request/task to determine log level and emit `[]slog.Attr`. |
| `clog.Run(ctx, event, fn)` / `clog.RunValue` | Boundary wrapper that records the Unit of Work's outcome (`-success` vs `-failure`) from `fn`'s returned error. Use inside framework handlers and RPC methods. Adapters should not record outcomes themselves. |
| `clog.ExtractTrace(sentryTrace, baggage)` | Extracts `traceId` and `spanId` from incoming headers or generates a random 32-char hex `traceId`. |

---

## 2. How to Write a Custom HTTP / Router Middleware

Standard `net/http` routers (Go 1.22+ `http.ServeMux`, Chi, Gorilla Mux) work out of the box with `clog.HTTPMiddleware` and `clog.HTTPTraceMiddleware`.

For frameworks with custom context types (like **Gin**, **Echo**, or **Fiber**), every canonical middleware drives the same 4-step **Wide Event per Unit of Work** lifecycle (see [`docs/architecture.md`](./architecture.md)):
1. **Open Unit of Work**: Create an isolated `FieldBag` via `ctx := clog.NewContext(reqCtx)` and attach it to the request.
2. **Execute & Record Outcome**: Call the next handler in the chain (`c.Next()` or `next(c)`), where handlers and services accumulate fields and boundary wrappers (`clog.Run` / `clog.RunValue`) record `-success` or `-failure` from returned errors.
3. **Evaluate Level**: Escalate to `slog.LevelError` if status >= 500, framework errors exist, or `bag.HasGroup("error")` is true; escalate to `slog.LevelWarn` if status >= 400 or `bag.HasWarning()` is true.
4. **Emit Wide Event**: Call `clog.LoggerFromContext(ctx).LogAttrs(ctx, level, bag.Message(), attrs...)` combining root transport attributes with `bag.Attrs()...`.

### Recipe: Gin Middleware & TraceID

```go
package middleware

import (
	"context"
	"log/slog"
	"time"

	"github.com/Shannor/canonical-logger"
	"github.com/gin-gonic/gin"
)

const ginFieldBagKey = "__clog_fieldbag__"

func init() {
	// Register a fallback resolver so clog.BagFromContext(ctx) still finds the FieldBag
	// even if downstream middleware replaces c.Request with a context containing only gin.ContextKey.
	clog.RegisterContextResolver(func(ctx context.Context) *clog.FieldBag {
		if ginCtx, ok := ctx.Value(gin.ContextKey).(*gin.Context); ok && ginCtx != nil {
			if val, exists := ginCtx.Get(ginFieldBagKey); exists {
				if bag, ok := val.(*clog.FieldBag); ok {
					return bag
				}
			}
		}
		return nil
	})
}

func CanonicalLogger(sensitiveParams ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		ctx := clog.NewContext(c.Request.Context())
		bag := clog.BagFromContext(ctx)

		c.Set(ginFieldBagKey, bag)
		c.Request = c.Request.WithContext(ctx)

		c.Next()

		stop := time.Since(start)
		path := c.Request.URL.Path
		for _, param := range sensitiveParams {
			if c.Param(param) != "" {
				if fullPath := c.FullPath(); fullPath != "" {
					path = fullPath
					break
				}
			}
		}

		status := c.Writer.Status()
		level := slog.LevelInfo
		if status >= 500 || len(c.Errors) > 0 || bag.HasGroup("error") {
			level = slog.LevelError
		} else if status >= 400 || bag.HasWarning() {
			level = slog.LevelWarn
		}

		attrs := []slog.Attr{
			slog.Int("status", status),
			slog.String("method", c.Request.Method),
			slog.String("path", path),
			slog.String("ip", c.ClientIP()),
			slog.String("latency", stop.String()),
			slog.String("user-agent", c.Request.UserAgent()),
		}
		if len(c.Errors) > 0 {
			attrs = append(attrs, slog.String("gin_error", c.Errors.String()))
		}
		attrs = append(attrs, bag.Attrs()...)

		clog.LoggerFromContext(c.Request.Context()).LogAttrs(
			c.Request.Context(),
			level,
			bag.Message(),
			attrs...,
		)
	}
}

func TraceID() gin.HandlerFunc {
	return func(c *gin.Context) {
		traceID, spanID := clog.ExtractTrace(
			c.GetHeader(clog.HeaderSentryTrace),
			c.GetHeader(clog.HeaderBaggage),
		)

		ctx := clog.WithField(c.Request.Context(), "traceId", traceID)
		if spanID != "" {
			ctx = clog.WithField(ctx, "spanId", spanID)
		}
		c.Request = c.Request.WithContext(ctx)

		c.Header(clog.ResponseTraceHeader, traceID)
		c.Next()
	}
}
```

---

## 3. How to Write a Custom `ContextResolver` (e.g., `oapi-codegen/gin-middleware`)

Some code generators or authentication middlewares (such as `github.com/oapi-codegen/gin-middleware`) construct a new `context.Context` and store the router context under their own context key.

Because `clog.RegisterContextResolver` accepts any `func(ctx context.Context) *clog.FieldBag`, you can register a resolver exclusively in services that use `oapi-codegen`—without coupling your non-OpenAPI services to `oapi-codegen`:

```go
package middleware

import (
	"context"

	"github.com/Shannor/canonical-logger"
	ginmiddleware "github.com/oapi-codegen/gin-middleware"
)

func init() {
	clog.RegisterContextResolver(func(ctx context.Context) *clog.FieldBag {
		if ginCtx := ginmiddleware.GetGinContext(ctx); ginCtx != nil {
			if val, exists := ginCtx.Get("__clog_fieldbag__"); exists {
				if bag, ok := val.(*clog.FieldBag); ok {
					return bag
				}
			}
		}
		return nil
	})
}
```

---

## 4. oapi-codegen Strict Server Recipe

[`oapi-codegen`](https://github.com/oapi-codegen/oapi-codegen) **strict servers** are the best fit for `clog`'s return-value result flow:
- every generated operation already has the `Run` shape: `GetUser(ctx, GetUserRequestObject) (GetUserResponseObject, error)`;
- strict middlewares receive the **operationId**, so **one middleware records the canonical outcome of every endpoint**, with no `clog.Run` calls in handler code.

> Verified end-to-end (every row of the table below) against code generated by `oapi-codegen` **v2.8.0** (`runtime` v1.7.0, `nethttp-middleware` v1.2.0, `gin-middleware` v1.1.0, `gin` v1.12.0), for both `std-http-server` and `gin-server` with `strict-server: true`.

### Resulting canonical lines

| Situation | Status | `message` | Level |
| --- | --- | --- | --- |
| Handler returns `GetUserById200JSONResponse{...}, nil` | 200 | `get-user-by-id-success` | INFO |
| Handler returns a typed 4xx (`GetUserById404JSONResponse{...}, nil`) | 404 | `get-user-by-id-success` (handled outcome) | WARN |
| Handler returns a typed 5xx (`GetUserById503JSONResponse{...}, nil`) | 503 | `get-user-by-id-failure`, and the typed body is still sent | ERROR |
| Handler returns `nil, err` (even after `x, err := ...` shadowing) | 500 | `get-user-by-id-failure` + `error.err` | ERROR |
| Path/query parameter fails to bind (before the handler) | 400 | `bind-request-params-failure` | ERROR |
| JSON body fails to decode (before the handler) | 400 | `decode-request-body-failure` | ERROR |
| OpenAPI request validation fails (`nethttp-middleware`) | 400 | `create-user-failure` (named from the matched operation) | ERROR |
| OpenAPI request validation fails (`gin-middleware`) | 400 | `validate-request-failure` (its hook doesn't expose the operation) | ERROR |

When a request validator is installed it runs **before** parameter binding, so malformed parameters (e.g. `/users/abc` for an integer `id`) are reported by the validator hook rather than `bind-request-params-failure`.

### Gotchas (read first)

1. **Use the generated types, not `runtime/strictmiddleware`.** Current `oapi-codegen` v2 defines `StrictHandlerFunc` / `StrictMiddlewareFunc` **inside each generated package**. The recipe is therefore generic over the handler type, and you **must instantiate it explicitly** (`oapiclog.StrictMiddleware[api.StrictHandlerFunc]`). Leaving the type parameter to inference doesn't work here; Go 1.27.2 even crashes with an internal compiler error.
2. **Put the clog middleware last** in the `[]StrictMiddlewareFunc` slice. Generated code wraps in slice order, so the last entry is outermost and also records errors returned by other strict middlewares (auth, tenancy, …).
3. **operationIds arrive in PascalCase** (`GetUserById`), whatever the spec says. `EventName` converts them to the `resource-action` convention (`get-user-by-id`).
4. **Typed responses carry no error.** By default typed 4xx responses are *handled outcomes* (`-success`, WARN via status) and typed 5xx are failures. Tune this with `IsFailureStatus`. For unexpected failures prefer `return nil, err` and render the response in the error handler. Status detection relies on oapi-codegen's `<Operation><Status><Media>Response` naming.
5. **Gin: handlers receive `*gin.Context` as `ctx`.** `clog.With*` calls inside strict handlers only reach the bag if you use the Gin recipe's `ContextResolver` (§2) or set `engine.ContextWithFallback = true`. Otherwise enrichment is **silently dropped**. The outcome is still recorded, because the middleware uses `c.Request.Context()`.
6. **Hooks that fire before the handler** (parameter binding, body decoding, request validation) have no function to wrap, so they record the error with `clog.Run(ctx, event, func(context.Context) error { return err })`. The error is a parameter, so it can't be shadowed. Keep this pattern inside these framework hooks and out of handlers.
7. **Not covered:** a failure while serializing a typed response (`Visit*Response`) happens after the outcome was recorded as success. The `status` field still reflects what was sent.
8. **Non-strict generated servers** (`GetUser(w, r, id)` / `GetUser(c *gin.Context, id)`) have no operationId hook. Wrap each handler body in `clog.Run` as in the Gin examples in the README.

### Recipe: `oapiclog` package (net/http: std-http, chi, gorilla)

```go
// Package oapiclog wires canonical-logger (clog) into oapi-codegen strict servers.
// Copy this file into your application; it is a recipe, not a library.
package oapiclog

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	clog "github.com/Shannor/canonical-logger"
	nethttpmiddleware "github.com/oapi-codegen/nethttp-middleware"
)

// IsFailureStatus decides which *typed* strict responses (e.g. GetUser503JSONResponse)
// are recorded as "-failure". Typed 4xx responses are handled outcomes by default.
var IsFailureStatus = func(status int) bool { return status >= 500 }

// StrictMiddleware records the canonical outcome of every strict operation, named after
// its operationId ("GetUserById" -> "get-user-by-id-success" / "-failure").
//
// It is generic over the generated StrictHandlerFunc type, so it works with any
// oapi-codegen std-http/chi/gorilla strict package:
//
//	stdapi.NewStrictHandlerWithOptions(server,
//		[]stdapi.StrictMiddlewareFunc{authMW, oapiclog.StrictMiddleware[stdapi.StrictHandlerFunc]},
//		opts)
//
// Put it LAST in the slice: the generated code applies middlewares in order, so the
// last one is outermost and also records errors returned by the other middlewares.
func StrictMiddleware[H ~func(context.Context, http.ResponseWriter, *http.Request, any) (any, error)](f H, operationID string) H {
	event := EventName(operationID)
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request, req any) (any, error) {
		return runStrict(ctx, event, func(ctx context.Context) (any, error) {
			return f(ctx, w, r, req)
		})
	}
}

// typedFailure marks a typed response object whose status IsFailureStatus.
type typedFailure struct {
	status int
	resp   any
}

func (e typedFailure) Error() string {
	return fmt.Sprintf("responded with typed %d response (%T)", e.status, e.resp)
}

// runStrict executes fn under clog.Run. Returned errors are recorded and passed on. Typed
// failure responses are recorded as failures but handed back to the generated code with
// a nil error, so it still writes the typed response.
func runStrict(ctx context.Context, event string, fn func(context.Context) (any, error)) (any, error) {
	var resp any
	err := clog.Run(ctx, event, func(ctx context.Context) error {
		var err error
		if resp, err = fn(ctx); err != nil {
			return err
		}
		if status, ok := ResponseStatus(resp); ok && IsFailureStatus(status) {
			return typedFailure{status: status, resp: resp}
		}
		return nil
	})
	if errors.As(err, new(typedFailure)) {
		return resp, nil
	}
	return resp, err
}

var statusInTypeName = regexp.MustCompile(`([1-5]\d\d)[A-Za-z]*Response$`)

// ResponseStatus extracts the HTTP status from a generated strict response object.
// It relies on oapi-codegen's naming (<Operation><Status><Media>Response) and on the
// StatusCode field of "default" responses.
func ResponseStatus(resp any) (int, bool) {
	if resp == nil {
		return 0, false
	}
	v := reflect.Indirect(reflect.ValueOf(resp))
	if m := statusInTypeName.FindStringSubmatch(v.Type().Name()); m != nil {
		status, _ := strconv.Atoi(m[1])
		return status, true
	}
	if v.Kind() == reflect.Struct {
		if f := v.FieldByName("StatusCode"); f.IsValid() && f.CanInt() {
			return int(f.Int()), true
		}
	}
	return 0, false
}

// EventName converts an operationId ("GetUserById", "getUserById", "get-user-by-id")
// into the clog resource-action convention ("get-user-by-id").
func EventName(operationID string) string {
	var b strings.Builder
	rs := []rune(operationID)
	for i, r := range rs {
		switch {
		case r == '_' || r == ' ' || r == '-':
			if b.Len() > 0 && !strings.HasSuffix(b.String(), "-") {
				b.WriteByte('-')
			}
			continue
		case unicode.IsUpper(r):
			prevLower := i > 0 && (unicode.IsLower(rs[i-1]) || unicode.IsDigit(rs[i-1]))
			acronymEnd := i > 0 && unicode.IsUpper(rs[i-1]) && i+1 < len(rs) && unicode.IsLower(rs[i+1])
			if (prevLower || acronymEnd) && !strings.HasSuffix(b.String(), "-") {
				b.WriteByte('-')
			}
			b.WriteRune(unicode.ToLower(r))
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// recordFailure records err as the outcome of event. Use it ONLY in framework error
// hooks that fire before (or instead of) a strict handler, where there is no function to
// wrap: the error is a parameter, so it cannot be shadowed.
func recordFailure(ctx context.Context, event string, err error) {
	_ = clog.Run(ctx, event, func(context.Context) error { return err })
}

// --- net/http (std-http, chi, gorilla) error hooks ---

// RequestErrorHandler is a StrictHTTPServerOptions.RequestErrorHandlerFunc. It fires
// when the request body cannot be decoded, before the strict middleware runs.
func RequestErrorHandler(w http.ResponseWriter, r *http.Request, err error) {
	recordFailure(r.Context(), "decode-request-body", err)
	http.Error(w, err.Error(), http.StatusBadRequest)
}

// ResponseErrorHandler is a StrictHTTPServerOptions.ResponseErrorHandlerFunc. Handler
// errors are already recorded by StrictMiddleware; this only renders the response.
func ResponseErrorHandler(w http.ResponseWriter, r *http.Request, err error) {
	http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
}

// ParamErrorHandler is a StdHTTPServerOptions/ChiServerOptions.ErrorHandlerFunc. It
// fires when path/query/header parameters fail to bind, before the strict handler.
func ParamErrorHandler(w http.ResponseWriter, r *http.Request, err error) {
	recordFailure(r.Context(), "bind-request-params", err)
	http.Error(w, err.Error(), http.StatusBadRequest)
}

// ValidationErrorHandler is a nethttp-middleware Options.ErrorHandlerWithOpts. The
// event is named after the matched operation when the route is known.
func ValidationErrorHandler(ctx context.Context, err error, w http.ResponseWriter, r *http.Request, opts nethttpmiddleware.ErrorHandlerOpts) {
	event := "validate-request"
	if mr := opts.MatchedRoute; mr != nil && mr.Route != nil && mr.Route.Operation != nil && mr.Route.Operation.OperationID != "" {
		event = EventName(mr.Route.Operation.OperationID)
	}
	recordFailure(ctx, event, err)
	status := opts.StatusCode
	if status == 0 {
		status = http.StatusBadRequest
	}
	http.Error(w, err.Error(), status)
}
```

### Recipe: Gin additions

```go
package oapiclog

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
)

// GinStrictMiddleware is the Gin form of StrictMiddleware:
//
//	ginapi.NewStrictHandlerWithOptions(server,
//		[]ginapi.StrictMiddlewareFunc{oapiclog.GinStrictMiddleware[ginapi.StrictHandlerFunc]},
//		ginapi.StrictGinServerOptions{
//			RequestErrorHandlerFunc: oapiclog.GinRequestErrorHandler,
//			HandlerErrorFunc:        oapiclog.GinHandlerErrorHandler,
//		})
func GinStrictMiddleware[H ~func(*gin.Context, any) (any, error)](f H, operationID string) H {
	event := EventName(operationID)
	return func(c *gin.Context, req any) (any, error) {
		return runStrict(c.Request.Context(), event, func(context.Context) (any, error) {
			return f(c, req)
		})
	}
}

// GinRequestErrorHandler is a StrictGinServerOptions.RequestErrorHandlerFunc.
func GinRequestErrorHandler(c *gin.Context, err error) {
	recordFailure(c.Request.Context(), "decode-request-body", err)
	c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
}

// GinHandlerErrorHandler is a StrictGinServerOptions.HandlerErrorFunc. The error is
// already recorded by GinStrictMiddleware; this only renders the response.
func GinHandlerErrorHandler(c *gin.Context, err error) {
	c.JSON(http.StatusInternalServerError, gin.H{"error": http.StatusText(http.StatusInternalServerError)})
}

// GinParamErrorHandler is a GinServerOptions.ErrorHandler (parameter binding).
func GinParamErrorHandler(c *gin.Context, err error, status int) {
	recordFailure(c.Request.Context(), "bind-request-params", err)
	c.JSON(status, gin.H{"error": err.Error()})
}

// GinValidationErrorHandler is a gin-middleware Options.ErrorHandler.
func GinValidationErrorHandler(c *gin.Context, message string, status int) {
	recordFailure(c.Request.Context(), "validate-request", errors.New(message))
	c.AbortWithStatusJSON(status, gin.H{"error": message})
}
```

### Wiring: std-http / chi strict server

```go
strict := api.NewStrictHandlerWithOptions(server,
	[]api.StrictMiddlewareFunc{authMiddleware, oapiclog.StrictMiddleware[api.StrictHandlerFunc]}, // clog LAST
	api.StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  oapiclog.RequestErrorHandler,
		ResponseErrorHandlerFunc: oapiclog.ResponseErrorHandler,
	})

spec, _ := api.GetSwagger()
spec.Servers = nil // see oapi-codegen#882

handler := api.HandlerWithOptions(strict, api.StdHTTPServerOptions{
	BaseRouter:       http.NewServeMux(),
	ErrorHandlerFunc: oapiclog.ParamErrorHandler,
	Middlewares: []api.MiddlewareFunc{
		nethttpmiddleware.OapiRequestValidatorWithOptions(spec, &nethttpmiddleware.Options{
			ErrorHandlerWithOpts: oapiclog.ValidationErrorHandler,
		}),
	},
})

// clog.HTTPMiddleware (or your own emitter) must wrap everything so the bag exists.
_ = http.ListenAndServe(":8080", clog.HTTPMiddleware(clog.HTTPTraceMiddleware(handler)))
```

### Wiring: Gin strict server

```go
r := gin.New()
r.ContextWithFallback = true // or rely on the Gin recipe's ContextResolver (§2)
r.Use(middleware.TraceID(), middleware.CanonicalLogger())
r.Use(ginmiddleware.OapiRequestValidatorWithOptions(spec, &ginmiddleware.Options{
	ErrorHandler: oapiclog.GinValidationErrorHandler,
}))

strict := api.NewStrictHandlerWithOptions(server,
	[]api.StrictMiddlewareFunc{oapiclog.GinStrictMiddleware[api.StrictHandlerFunc]},
	api.StrictGinServerOptions{
		RequestErrorHandlerFunc: oapiclog.GinRequestErrorHandler,
		HandlerErrorFunc:        oapiclog.GinHandlerErrorHandler,
	})
api.RegisterHandlersWithOptions(r, strict, api.GinServerOptions{
	ErrorHandler: oapiclog.GinParamErrorHandler,
})
```

Handlers then contain only business logic, and every outcome comes from the return value:

```go
func (s Server) GetUserById(ctx context.Context, req api.GetUserByIdRequestObject) (api.GetUserByIdResponseObject, error) {
	clog.WithGroupField(ctx, "request", "userId", req.Id)

	user, err := s.users.Get(ctx, req.Id)
	if errors.Is(err, ErrNotFound) {
		return api.GetUserById404JSONResponse{Message: "user not found"}, nil // handled: -success, WARN
	}
	if err != nil {
		return nil, err // -> get-user-by-id-failure
	}
	return api.GetUserById200JSONResponse(user), nil
}
```

---

## 5. How to Write a gRPC Unary Server Interceptor

The exact same pattern applies to gRPC servers:

```go
package grpclog

import (
	"context"
	"log/slog"
	"time"

	"github.com/Shannor/canonical-logger"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

func UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		start := time.Now()
		ctx = clog.NewContext(ctx)
		bag := clog.BagFromContext(ctx)

		resp, err := handler(ctx, req)

		stop := time.Since(start)
		code := status.Code(err)

		level := slog.LevelInfo
		if err != nil || bag.HasGroup("error") {
			level = slog.LevelError
		} else if bag.HasWarning() {
			level = slog.LevelWarn
		}

		attrs := []slog.Attr{
			slog.String("grpc.method", info.FullMethod),
			slog.String("grpc.code", code.String()),
			slog.String("latency", stop.String()),
		}
		attrs = append(attrs, bag.Attrs()...)

		clog.LoggerFromContext(ctx).LogAttrs(ctx, level, bag.Message(), attrs...)
		return resp, err
	}
}
```

---

## 6. How to Write a Custom Log Sink / `io.Writer`

Because `clog` is built on standard `log/slog`, any `io.Writer` or `slog.Handler` works with `clog.NewJSONHandler(w, opts)`.

### Option A: Zero-Dependency Stdout JSON (Recommended for Cloud Run, GKE, Kubernetes, AWS ECS)

Container platforms automatically ingest structured JSON from `os.Stdout`. For Google Cloud Logging, `clog.GCPReplaceAttr` is built into the zero-dependency core:

```go
handler := clog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
	ReplaceAttr: clog.GCPReplaceAttr,
})
slog.SetDefault(slog.New(handler))
```

### Option B: Custom Google Cloud Logging SDK `io.Writer`

If your application streams logs directly via the `cloud.google.com/go/logging` SDK rather than `os.Stdout`, implement `io.Writer` in your application's logging setup:

```go
package gcpwriter

import (
	"encoding/json"
	"io"

	"cloud.google.com/go/logging"
)

type GCPWriter struct {
	Logger *logging.Logger
}

func (w *GCPWriter) Write(p []byte) (int, error) {
	if w.Logger == nil {
		return len(p), nil
	}

	fields := make(map[string]any)
	if err := json.Unmarshal(p, &fields); err != nil {
		w.Logger.Log(logging.Entry{Payload: string(p)})
		return len(p), nil
	}

	entry := logging.Entry{Payload: fields}
	if sev, ok := fields["severity"].(string); ok {
		entry.Severity = logging.ParseSeverity(sev)
	} else if lvl, ok := fields["level"].(string); ok {
		entry.Severity = logging.ParseSeverity(lvl)
	}

	w.Logger.Log(entry)
	return len(p), nil
}

var _ io.Writer = (*GCPWriter)(nil)
```
