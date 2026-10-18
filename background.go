package clog

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// NewBackground creates a fresh canonical log context for use outside of HTTP requests.
// Returns a context with a FieldBag attached and a flush func.
// The flush func emits the canonical log line and is safe to call multiple times (only emits once).
// Prefer CronFunc, Func and Go over calling this directly. If you do use it, record the
// outcome with Run.
func NewBackground(name string) (context.Context, func()) {
	return newBackground(context.Background(), name)
}

func newBackground(parentCtx context.Context, name string) (context.Context, func()) {
	start := time.Now()
	baseCtx := context.Background()
	if parentCtx != nil {
		baseCtx = context.WithoutCancel(parentCtx)
	}
	ctx := NewContext(baseCtx)
	bag := BagFromContext(ctx)

	var once sync.Once
	flush := func() {
		once.Do(func() {
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
		})
	}
	return ctx, flush
}

// Func runs fn synchronously with its own canonical logging session and returns fn's
// error. The returned error is recorded as the outcome for name ("<name>-success" /
// "<name>-failure"), and the canonical log line is flushed when fn returns. Use for
// one-off synchronous tasks that are not cron jobs or goroutines (e.g. startup tasks):
//
//	if err := clog.Func("startup-migrations", migrate); err != nil {
//		os.Exit(1)
//	}
func Func(name string, fn func(ctx context.Context) error) error {
	ctx, flush := NewBackground(name)
	defer flush()
	return Run(ctx, name, fn)
}

// CronFunc wraps fn for use as a cron job body. Returns a func() compatible with
// cron.FuncJob or direct invocation. fn's returned error is recorded as the outcome for
// name, and the canonical log line is flushed when fn returns.
func CronFunc(name string, fn func(ctx context.Context) error) func() {
	return func() {
		_ = Func(name, fn)
	}
}

// Go spawns a goroutine with its own canonical logging session, detached from the
// parent context. fn's returned error is recorded as the outcome for name. The goroutine:
//   - Is NOT cancelled when the parent context is cancelled (context.WithoutCancel)
//   - Inherits any custom *slog.Logger attached to the parent context
//   - Has its own fresh FieldBag for an independent canonical log line
//   - Copies the full parent FieldBag as a "parent" group for correlation
//   - Recovers panics and records them as "<name>-failure"
//
// The canonical log line is automatically flushed when fn returns.
func Go(parentCtx context.Context, name string, fn func(ctx context.Context) error) {
	// Snapshot parent bag and logger before detaching; safe if parentCtx is nil.
	parentSnap := BagFromContext(parentCtx).snapshot()
	parentLogger := rawLoggerFromContext(parentCtx)

	go func() {
		ctx, flush := newBackground(parentCtx, name)
		if parentLogger != nil {
			ctx = NewContextWithLogger(ctx, parentLogger)
		}
		defer flush()

		// Recover from panics so the canonical log line is emitted. Run has already
		// recorded the panic as the failure outcome before re-panicking.
		defer func() {
			if r := recover(); r != nil {
				err := fmt.Errorf("panic in background goroutine %q: %v", name, r)
				LoggerFromContext(ctx).ErrorContext(ctx, "recovered panic in background goroutine",
					slog.String("job", name),
					slog.String("error", err.Error()),
				)
			}
		}()

		// Attach parent snapshot as "parent" group for log correlation.
		if len(parentSnap) > 0 {
			bag := BagFromContext(ctx)
			bag.setGroupMany("parent", parentSnap)
		}

		_ = Run(ctx, name, fn)
	}()
}
