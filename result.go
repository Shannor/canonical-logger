package clog

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"sync"
	"time"
)

// ErrorFormatter extracts structured metadata fields from an error for the "error" group.
type ErrorFormatter func(err error) Fields

var (
	errorFormatterMu sync.RWMutex
	errorFormatter   ErrorFormatter = DefaultErrorFormatter
)

// SetErrorFormatter configures the ErrorFormatter used when Run records a failure.
// Passing nil restores DefaultErrorFormatter.
func SetErrorFormatter(fn ErrorFormatter) {
	errorFormatterMu.Lock()
	defer errorFormatterMu.Unlock()
	if fn == nil {
		errorFormatter = DefaultErrorFormatter
		return
	}
	errorFormatter = fn
}

func getErrorFormatter() ErrorFormatter {
	errorFormatterMu.RLock()
	defer errorFormatterMu.RUnlock()
	return errorFormatter
}

// DefaultErrorFormatter extracts the error message ("err"), root cause ("cause" when wrapped),
// and optional Code(), Category(), and ErrorFields() metadata from the error chain.
func DefaultErrorFormatter(err error) Fields {
	if err == nil {
		return nil
	}
	fields := Fields{
		"err": err.Error(),
	}

	var fErr interface{ ErrorFields() map[string]any }
	if errors.As(err, &fErr) {
		if extra := fErr.ErrorFields(); len(extra) > 0 {
			maps.Copy(fields, extra)
		}
	}

	var codeErr interface{ Code() string }
	if errors.As(err, &codeErr) {
		if code := codeErr.Code(); code != "" {
			fields["code"] = code
		}
	}

	var catErr interface{ Category() string }
	if errors.As(err, &catErr) {
		if cat := catErr.Category(); cat != "" {
			fields["category"] = cat
		}
	}

	if cause := unwrapRootCause(err); cause != nil && cause != err {
		fields["cause"] = cause.Error()
	}

	return fields
}

func unwrapRootCause(err error) error {
	curr := err
	for {
		unwrapped := errors.Unwrap(curr)
		if unwrapped == nil {
			return curr
		}
		curr = unwrapped
	}
}

// Run executes fn as a unit-of-work boundary operation, recording its returned error as
// the outcome of the canonical log line:
//
//   - fn returns nil:     message becomes "<baseEvent>-success".
//   - fn returns an error: message becomes "<baseEvent>-failure", the line escalates to
//     ERROR, and the "error" group is populated via the active ErrorFormatter.
//
// If ctx does not already carry a FieldBag (for example, when no HTTP/RPC middleware is
// installed, or in a standalone task, CLI command, or consumer), Run opens a fresh
// FieldBag on the ctx passed to fn and flushes the canonical log line (with "job" and
// "latency") when fn returns. If ctx already carries a FieldBag (opened by a transport
// middleware, Go, or an enclosing Run), Run records the outcome on that existing bag and
// lets the outermost owner flush once at boundary exit.
//
// Run is the single primitive for recording outcomes. Because the outcome is taken from
// fn's return value rather than from a captured variable, := shadowing inside fn cannot
// produce a false success. Use it at boundary layers only (API handlers, webhooks,
// consumers, RPC handlers, synchronous tasks); Go, RunFunc, RunValue, and HTTPHandler
// are built on it.
//
// Failure is sticky: if a failure was already recorded on the bag (e.g. by a nested Run)
// or an "error" group was attached (e.g. by a router adapter), a nil result does not
// downgrade the line to "-success". When Runs are nested, the outermost Run owns the
// event name.
//
// If fn panics, the panic is recorded as a failure (and flushed if Run opened the bag)
// and then re-panicked so upstream recovery (http.Server, Go, cron schedulers) behaves
// as before.
//
//	func (api API) Create(c *gin.Context) {
//		_ = clog.Run(c.Request.Context(), "create-campaign", func(ctx context.Context) error {
//			req, err := bind(c) // shadowing is harmless
//			if err != nil {
//				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
//				return err
//			}
//			return api.svc.Create(ctx, req)
//		})
//	}
func Run(ctx context.Context, baseEvent string, fn func(ctx context.Context) error) (err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var (
		ownBag *FieldBag
		start  time.Time
	)
	if BagFromContext(ctx) == nil {
		start = time.Now()
		ctx = NewContext(ctx)
		ownBag = BagFromContext(ctx)
	}
	defer func() {
		if r := recover(); r != nil {
			recordResult(ctx, baseEvent, panicError(baseEvent, r))
			if ownBag != nil {
				flushBag(ctx, ownBag, baseEvent, start)
			}
			panic(r)
		}
		recordResult(ctx, baseEvent, err)
		if ownBag != nil {
			flushBag(ctx, ownBag, baseEvent, start)
		}
	}()
	return fn(ctx)
}

func flushBag(ctx context.Context, bag *FieldBag, name string, start time.Time) {
	latency := time.Since(start)
	logger := LoggerFromContext(ctx)
	level := slog.LevelInfo
	if bag.hasGroup("error") {
		level = slog.LevelError
	} else if bag.hasWarning() {
		level = slog.LevelWarn
	}

	baseAttrs := [2]slog.Attr{
		slog.String("job", name),
		slog.String("latency", latency.String()),
	}
	attrs := bag.appendAttrs(baseAttrs[:])

	logger.LogAttrs(ctx, level, bag.Message(), attrs...)
}

// RunValue is the value-returning form of Run, convenient for RPC handlers:
//
//	func (s Server) GetUser(ctx context.Context, req *pb.GetUserRequest) (*pb.User, error) {
//		return clog.RunValue(ctx, "get-user", func(ctx context.Context) (*pb.User, error) {
//			return s.users.Get(ctx, req.GetId())
//		})
//	}
func RunValue[T any](ctx context.Context, baseEvent string, fn func(ctx context.Context) (T, error)) (T, error) {
	var v T
	err := Run(ctx, baseEvent, func(ctx context.Context) error {
		var err error
		v, err = fn(ctx)
		return err
	})
	return v, err
}

// HTTPHandlerFunc is an HTTP handler that returns its outcome as an error (the
// "appHandler" pattern from https://go.dev/blog/error-handling-and-go).
// The handler is still responsible for writing the HTTP response.
type HTTPHandlerFunc func(w http.ResponseWriter, r *http.Request) error

// HTTPHandler adapts an error-returning HTTPHandlerFunc into an http.Handler that records the
// returned error as the canonical outcome for baseEvent (via Run).
//
//	mux.Handle("POST /campaigns", clog.HTTPHandler("create-campaign", func(w http.ResponseWriter, r *http.Request) error {
//		data, err := api.service.Process(r.Context()) // shadowing is harmless
//		if err != nil {
//			http.Error(w, "internal error", http.StatusInternalServerError)
//			return err // recorded as create-campaign-failure
//		}
//		return json.NewEncoder(w).Encode(data)
//	}))
func HTTPHandler(baseEvent string, fn HTTPHandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rCtx := r.Context()
		_ = Run(rCtx, baseEvent, func(ctx context.Context) error {
			if ctx != rCtx {
				r = r.WithContext(ctx)
			}
			return fn(w, r)
		})
	})
}

// recordResult applies an outcome to the bag in ctx. It is the internal implementation
// behind Run; it is intentionally unexported so outcomes can only be derived from return
// values.
func recordResult(ctx context.Context, baseEvent string, err error) {
	bag := BagFromContext(ctx)
	if bag == nil {
		return
	}
	var fields Fields
	if err != nil {
		// Run the (user-supplied) formatter outside the bag lock.
		fields = getErrorFormatter()(err)
		if len(fields) == 0 {
			fields = Fields{"err": err.Error()}
		}
	}
	bag.setResult(baseEvent, err != nil, fields)
}

// setResult atomically applies an outcome to the bag. errFields is only applied when
// failed is true and no "error" group exists yet.
func (b *FieldBag) setResult(baseEvent string, failed bool, errFields Fields) {
	b.mu.Lock()
	defer b.mu.Unlock()

	_, hasErrGroup := b.groups["error"]
	if failed {
		if !hasErrGroup && len(errFields) > 0 {
			maps.Copy(b.ensurePath("error"), errFields)
		}
		b.outcome = outcomeFailure
		b.message = baseEvent + "-failure"
		return
	}

	// Sticky failure: never downgrade a recorded failure (or an attached error group).
	if b.outcome == outcomeFailure {
		return
	}
	if hasErrGroup {
		b.outcome = outcomeFailure
		b.message = baseEvent + "-failure"
		return
	}
	b.outcome = outcomeSuccess
	b.message = baseEvent + "-success"
}

func panicError(name string, r any) error {
	if e, ok := r.(error); ok {
		return fmt.Errorf("panic in %q: %w", name, e)
	}
	return fmt.Errorf("panic in %q: %v", name, r)
}
