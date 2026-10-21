package clog

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// syncWriter is a mutex-protected io.Writer used so that concurrent goroutines
// (the test's background goroutine flushing its canonical line) can safely write
// to the capture buffer while the test reads from it after synchronization.
type syncWriter struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *syncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *syncWriter) Bytes() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	b := make([]byte, w.buf.Len())
	copy(b, w.buf.Bytes())
	return b
}

// captureBackground redirects the default slog logger to a thread-safe buffer
// for the duration of fn, then returns all JSON lines emitted.
func captureBackground(t *testing.T, fn func()) []map[string]any {
	t.Helper()
	w := &syncWriter{}
	orig := slog.Default()
	h := NewJSONHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug})
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(orig) })

	fn()

	raw := w.Bytes()
	var lines []map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var m map[string]any
		require.NoError(t, json.Unmarshal(line, &m))
		lines = append(lines, m)
	}
	return lines
}

// goWait sleeps briefly to allow a goroutine spawned by xlog.Go to execute its
// deferred flush() after its fn body returns.
func goWait() { time.Sleep(20 * time.Millisecond) }

// --- NewBackground ---

func TestNewBackground_ReturnsContextWithBag(t *testing.T) {
	ctx, flush := NewBackground("test-job")
	defer flush()

	bag := BagFromContext(ctx)
	require.NotNil(t, bag, "NewBackground must attach a FieldBag to the returned context")
}

func TestNewBackground_FlushEmitsCanonicalLine(t *testing.T) {
	lines := captureBackground(t, func() {
		_, flush := NewBackground("flush-test")
		flush()
	})

	require.Len(t, lines, 1)
	assert.Equal(t, "flush-test", lines[0]["job"])
	assert.Equal(t, "canonical-log-line", lines[0]["message"])
	assert.NotEmpty(t, lines[0]["latency"])
}

func TestNewBackground_FlushIncludesFieldBagFields(t *testing.T) {
	lines := captureBackground(t, func() {
		ctx, flush := NewBackground("enriched-job")
		WithField(ctx, "rows", 42)
		WithGroupFields(ctx, "error", map[string]any{"msg": "oops"})
		flush()
	})

	require.Len(t, lines, 1)
	line := lines[0]
	assert.Equal(t, float64(42), line["rows"])
	errGroup, ok := line["error"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "oops", errGroup["msg"])
}

func TestNewBackground_FlushOnceOnly(t *testing.T) {
	lines := captureBackground(t, func() {
		_, flush := NewBackground("once-job")
		flush()
		flush() // second call must be a no-op
	})

	assert.Len(t, lines, 1, "flush must emit exactly one canonical line regardless of how many times it is called")
}

// --- Run (standalone synchronous task) ---

func TestRun_StandaloneEmitsCanonicalLine(t *testing.T) {
	lines := captureBackground(t, func() {
		_ = Run(context.Background(), "startup-task", func(ctx context.Context) error { return nil })
	})

	require.Len(t, lines, 1)
	assert.Equal(t, "startup-task", lines[0]["job"])
	assert.Equal(t, "startup-task-success", lines[0]["message"], "outcome is recorded from fn's return value")
	assert.NotEmpty(t, lines[0]["latency"])
}

func TestRun_StandaloneContextHasBag(t *testing.T) {
	var captured *FieldBag
	captureBackground(t, func() {
		_ = Run(context.Background(), "bag-func", func(ctx context.Context) error {
			captured = BagFromContext(ctx)
			return nil
		})
	})
	require.NotNil(t, captured, "ctx passed to Run fn must have a FieldBag")
}

func TestRun_StandaloneFieldsAppearedInCanonicalLine(t *testing.T) {
	lines := captureBackground(t, func() {
		_ = Run(context.Background(), "fields-func", func(ctx context.Context) error {
			WithField(ctx, "records_synced", 7)
			return nil
		})
	})

	require.Len(t, lines, 1)
	assert.Equal(t, float64(7), lines[0]["records_synced"])
}

func TestRun_StandaloneRunsSynchronously(t *testing.T) {
	ran := false
	_ = Run(context.Background(), "sync-func", func(ctx context.Context) error {
		ran = true
		return nil
	})
	assert.True(t, ran, "Run must run fn before returning")
}

// --- RunFunc ---

func TestRunFunc_ReturnsCallable(t *testing.T) {
	fn := RunFunc("cron-job", func(ctx context.Context) error { return nil })
	require.NotNil(t, fn)
}

func TestRunFunc_EmitsCanonicalLine(t *testing.T) {
	lines := captureBackground(t, func() {
		RunFunc("my-cron", func(ctx context.Context) error { return nil })()
	})

	require.Len(t, lines, 1)
	assert.Equal(t, "my-cron", lines[0]["job"])
	assert.Equal(t, "my-cron-success", lines[0]["message"], "outcome is recorded from fn's return value")
}

func TestRunFunc_ContextHasBag(t *testing.T) {
	var captured *FieldBag
	captureBackground(t, func() {
		RunFunc("bag-cron", func(ctx context.Context) error {
			captured = BagFromContext(ctx)
			return nil
		})()
	})
	require.NotNil(t, captured, "ctx passed to RunFunc fn must have a FieldBag")
}

func TestRunFunc_FieldsAppearedInCanonicalLine(t *testing.T) {
	lines := captureBackground(t, func() {
		RunFunc("fields-cron", func(ctx context.Context) error {
			WithField(ctx, "users_processed", 10)
			return nil
		})()
	})

	require.Len(t, lines, 1)
	assert.Equal(t, float64(10), lines[0]["users_processed"])
}

// --- Go ---

func TestGo_SpawnsGoroutine(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)

	captureBackground(t, func() {
		Go(context.Background(), "test-go", func(ctx context.Context) error {
			wg.Done()
			return nil
		})
		wg.Wait()
		goWait()
	})
}

func TestGo_EmitsCanonicalLine(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)

	lines := captureBackground(t, func() {
		Go(context.Background(), "go-job", func(ctx context.Context) error {
			wg.Done()
			return nil
		})
		wg.Wait()
		goWait()
	})

	require.Len(t, lines, 1)
	assert.Equal(t, "go-job", lines[0]["job"])
	assert.Equal(t, "go-job-success", lines[0]["message"], "outcome is recorded from fn's return value")
	assert.NotEmpty(t, lines[0]["latency"])
}

func TestGo_ContextHasBag(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	var captured *FieldBag

	captureBackground(t, func() {
		Go(context.Background(), "bag-go", func(ctx context.Context) error {
			captured = BagFromContext(ctx)
			wg.Done()
			return nil
		})
		wg.Wait()
		goWait()
	})

	require.NotNil(t, captured)
}

func TestGo_ContextNotCancelledWhenParentCancelled(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	var innerErr error

	captureBackground(t, func() {
		Go(parent, "detach-go", func(ctx context.Context) error {
			cancel()
			time.Sleep(5 * time.Millisecond)
			innerErr = ctx.Err()
			wg.Done()
			return nil
		})
		wg.Wait()
		goWait()
	})

	assert.NoError(t, innerErr, "goroutine context must not be cancelled when parent is cancelled")
}

func TestGo_ParentBagSnapshotted(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)

	lines := captureBackground(t, func() {
		parentCtx := NewContext(context.Background())
		WithField(parentCtx, "actor_id", "u1")
		WithGroupFields(parentCtx, "campaign", map[string]any{"id": "c1"})

		Go(parentCtx, "child-go", func(ctx context.Context) error {
			wg.Done()
			return nil
		})
		wg.Wait()
		goWait()
	})

	require.Len(t, lines, 1)
	parent, ok := lines[0]["parent"].(map[string]any)
	require.True(t, ok, "canonical line must contain a 'parent' group with the parent bag snapshot")
	assert.Equal(t, "u1", parent["actor_id"])
	campaign, ok := parent["campaign"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "c1", campaign["id"])
}

func TestGo_NilParentBag_NoParentGroup(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)

	lines := captureBackground(t, func() {
		Go(context.Background(), "no-parent-go", func(ctx context.Context) error {
			wg.Done()
			return nil
		})
		wg.Wait()
		goWait()
	})

	require.Len(t, lines, 1)
	assert.NotContains(t, lines[0], "parent")
}

// --- Go panic recovery ---

func TestGo_RecoversPanic_EmitsErrorLog(t *testing.T) {
	lines := captureBackground(t, func() {
		var wg sync.WaitGroup
		wg.Add(1)

		Go(context.Background(), "panic-job", func(ctx context.Context) error {
			defer wg.Done()
			panic("something went wrong")
		})
		wg.Wait()
		goWait()
	})

	require.Len(t, lines, 2, "expected error log + canonical log line")

	var errorLine map[string]any
	var canonicalLine map[string]any
	for _, l := range lines {
		if l["message"] == "recovered panic in background goroutine" {
			errorLine = l
		} else {
			canonicalLine = l
		}
	}

	require.NotNil(t, errorLine, "must emit an error-level log")
	assert.Contains(t, errorLine["error"], "panic in background goroutine")
	assert.Contains(t, errorLine["error"], "something went wrong")
	assert.Equal(t, "panic-job", errorLine["job"])
	assert.Equal(t, "recovered panic in background goroutine", errorLine["message"])

	require.NotNil(t, canonicalLine, "must still emit canonical log line")
	assert.Equal(t, "panic-job", canonicalLine["job"])
}

func TestGo_RecoversPanic_SetsResult(t *testing.T) {
	lines := captureBackground(t, func() {
		var wg sync.WaitGroup
		wg.Add(1)

		Go(context.Background(), "panic-result", func(ctx context.Context) error {
			defer wg.Done()
			panic("bad data")
		})
		wg.Wait()
		goWait()
	})

	require.Len(t, lines, 2)

	var canonicalLine map[string]any
	for _, l := range lines {
		if l["message"] == "panic-result-failure" {
			canonicalLine = l
		}
	}
	require.NotNil(t, canonicalLine)
	assert.Contains(t, canonicalLine["message"], "panic-result-failure")
}

func TestBackground_ErrorLogLevel(t *testing.T) {
	lines := captureBackground(t, func() {
		ctx, flush := NewBackground("error-job")
		WithGroupFields(ctx, "error", map[string]any{"msg": "something broke"})
		flush()
	})

	require.Len(t, lines, 1)
	assert.Equal(t, "ERROR", lines[0]["level"])
}

func TestBackground_WarningLevel(t *testing.T) {
	lines := captureBackground(t, func() {
		ctx, flush := NewBackground("warn-job")
		WithGroupFields(ctx, "warning", map[string]any{"msg": "something is fishy"})
		flush()
	})

	require.Len(t, lines, 1)
	assert.Equal(t, "WARN", lines[0]["level"])
}

func TestBackground_ErrorTakesPrecedenceOverWarning(t *testing.T) {
	lines := captureBackground(t, func() {
		ctx, flush := NewBackground("both-job")
		WithField(ctx, "warning", "minor issue")
		WithGroupFields(ctx, "error", map[string]any{"msg": "major issue"})
		flush()
	})

	require.Len(t, lines, 1)
	assert.Equal(t, "ERROR", lines[0]["level"])
}

func TestGo_InheritsParentContextLogger(t *testing.T) {
	w := &syncWriter{}
	customLogger := slog.New(NewJSONHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug}).WithAttrs([]slog.Attr{
		slog.String("logger_scope", "custom-request"),
	}))

	parentCtx := NewContext(context.Background())
	parentCtx = NewContextWithLogger(parentCtx, customLogger)
	WithField(parentCtx, "requestId", "req-123")

	var wg sync.WaitGroup
	wg.Add(1)
	Go(parentCtx, "inherited-logger-job", func(ctx context.Context) error {
		defer wg.Done()
		WithField(ctx, "worker_step", "done")
		return nil
	})
	wg.Wait()
	goWait()

	var line map[string]any
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(w.Bytes()), &line))
	assert.Equal(t, "inherited-logger-job", line["job"])
	assert.Equal(t, "custom-request", line["logger_scope"])
	assert.Equal(t, "done", line["worker_step"])
}

func TestGo_PreservesParentContextValuesWithoutCancel(t *testing.T) {
	type customCtxKey struct{}
	parent, cancel := context.WithCancel(context.WithValue(context.Background(), customCtxKey{}, "tenant-42"))

	var wg sync.WaitGroup
	wg.Add(1)
	var gotVal any
	var gotErr error

	captureBackground(t, func() {
		Go(parent, "ctx-val-job", func(ctx context.Context) error {
			cancel()
			gotVal = ctx.Value(customCtxKey{})
			gotErr = ctx.Err()
			wg.Done()
			return nil
		})
		wg.Wait()
		goWait()
	})

	assert.Equal(t, "tenant-42", gotVal)
	assert.NoError(t, gotErr)
}

func TestRun_LoggerUpdatedInsideFn(t *testing.T) {
	w := &syncWriter{}
	customLogger := slog.New(NewJSONHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug}))

	_ = Run(context.Background(), "custom-logger-func", func(ctx context.Context) error {
		NewContextWithLogger(ctx, customLogger)
		WithField(ctx, "migrated", true)
		return nil
	})

	var line map[string]any
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(w.Bytes()), &line))
	assert.Equal(t, "custom-logger-func", line["job"])
	assert.Equal(t, true, line["migrated"])
}
