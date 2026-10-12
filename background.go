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
// Prefer CronFunc and Go over calling this directly.
func NewBackground(name string) (context.Context, func()) {
	start := time.Now()
	ctx := NewContext(context.Background())
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

			attrs := []slog.Attr{
				slog.String("job", name),
				slog.String("latency", latency.String()),
			}
			attrs = append(attrs, bag.Attrs()...)

			logger.LogAttrs(ctx, level, bag.Message(), attrs...)
		})
	}
	return ctx, flush
}

// Func runs fn synchronously with its own canonical logging session.
// Prefer this over calling NewBackground directly for one-off synchronous
// call sites that are not cron jobs or goroutines (e.g. startup tasks).
// The canonical log line is automatically flushed when fn returns.
func Func(name string, fn func(ctx context.Context)) {
	ctx, flush := NewBackground(name)
	defer flush()
	fn(ctx)
}

// CronFunc wraps a function for use as a cron job body.
// Returns a func() compatible with cron.FuncJob or direct invocation.
// The canonical log line is automatically flushed when fn returns.
func CronFunc(name string, fn func(ctx context.Context)) func() {
	return func() {
		ctx, flush := NewBackground(name)
		defer flush()
		fn(ctx)
	}
}

// Go spawns a goroutine with its own canonical logging session, detached from the
// parent context. The goroutine:
//   - Is NOT cancelled when the parent context is cancelled (context.WithoutCancel)
//   - Has its own fresh FieldBag for an independent canonical log line
//   - Copies the full parent FieldBag as a "parent" group for correlation
//
// The canonical log line is automatically flushed when fn returns.
func Go(parentCtx context.Context, name string, fn func(ctx context.Context)) {
	// Snapshot parent bag before detaching; nil if no bag in parent.
	parentSnap := BagFromContext(parentCtx).snapshot()

	go func() {
		ctx, flush := NewBackground(name)
		defer flush()

		// Recover from panics so the canonical log line is emitted and the
		// error is visible in structured logs.
		defer func() {
			if r := recover(); r != nil {
				err := fmt.Errorf("panic in background goroutine %q: %v", name, r)
				SetResult(ctx, name, err)
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

		fn(ctx)
	}()
}
