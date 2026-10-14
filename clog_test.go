package clog

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLevelSampler(t *testing.T) {
	tests := []struct {
		name       string
		sampleRate float64
		level      slog.Level
		expected   bool
	}{
		{"Always allow Warn", 0.0, LevelWarn, true},
		{"Always allow Error", 0.0, LevelError, true},
		{"Always allow Fatal", 0.0, LevelFatal, true},
		{"Always allow Panic", 0.0, LevelPanic, true},
		{"Sampling 100% Info", 1.0, LevelInfo, true},
		{"Sampling 0% Info", 0.0, LevelInfo, false},
		{"Sampling 0% Debug", 0.0, LevelDebug, false},
		{"Sampling 0% Trace", 0.0, LevelTrace, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := LevelSampler{SampleRate: tt.sampleRate}
			assert.Equal(t, tt.expected, s.Sample(tt.level))
		})
	}
}

func TestSamplingHandler(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewJSONHandler(&buf, nil)
	sampler := NewSamplingHandler(inner, 0.0) // Drop all Info

	logger := slog.New(sampler)
	logger.Info("should be dropped")
	assert.Empty(t, buf.String())

	logger.Warn("should be kept")
	assert.Contains(t, buf.String(), "should be kept")
}

func TestSamplingHandler_SharesCounterAcrossDerivedLoggers(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewJSONHandler(&buf, nil)
	// Sample 1 out of every 5 Info logs (20%)
	sampler := NewSamplingHandler(inner, 0.2)
	baseLogger := slog.New(sampler)

	for i := 0; i < 10; i++ {
		// Create a fresh derived logger per iteration via With and WithGroup
		reqLogger := baseLogger.With(slog.Int("req", i)).WithGroup("meta")
		reqLogger.Info("request completed")
	}

	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	require.Len(t, lines, 2, "expected exactly 2 of 10 logs to be emitted across derived loggers")
}

func TestSamplingHandler_DisabledInnerLevelDoesNotAdvanceCounter(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	// Sample 1 out of every 2 logs (50%)
	sampler := NewSamplingHandler(inner, 0.5)
	logger := slog.New(sampler)

	// Debug logs are disabled by inner handler and must not advance the counter
	logger.Debug("debug 1")
	logger.Debug("debug 2")
	logger.Debug("debug 3")

	// First Info log sets counter=1 (dropped), second Info log sets counter=2 (emitted)
	logger.Info("info 1")
	assert.Empty(t, buf.String())

	logger.Info("info 2")
	assert.Contains(t, buf.String(), "info 2")
}

func TestSamplingHandlerWithEvaluator(t *testing.T) {
	var buf bytes.Buffer
	inner := NewJSONHandler(&buf, nil)
	sampler := NewSamplingHandlerWithEvaluator(inner, 0.0, func(ctx context.Context, bag *FieldBag) bool {
		if bag == nil {
			return false
		}
		actor := bag.getGroup("actor")
		return actor["tier"] == "premium"
	})
	logger := slog.New(sampler)

	// Free tier should be dropped
	freeCtx := NewContext(context.Background())
	WithGroupField(freeCtx, "actor", "tier", "free")
	logger.InfoContext(freeCtx, "free request")
	assert.Empty(t, buf.String())

	// Premium tier should be sampled
	premiumCtx := NewContext(context.Background())
	WithGroupField(premiumCtx, "actor", "tier", "premium")
	logger.InfoContext(premiumCtx, "premium request")
	assert.Contains(t, buf.String(), "premium request")

	// Warn is always emitted regardless of evaluator
	buf.Reset()
	logger.WarnContext(freeCtx, "free warning")
	assert.Contains(t, buf.String(), "free warning")
}

func TestNewJSONHandler_MessageKeyReplacement(t *testing.T) {
	var buf bytes.Buffer
	h := NewJSONHandler(&buf, nil)
	logger := slog.New(h)

	logger.Info("hello world")

	var m map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &m))
	assert.Equal(t, "hello world", m["message"])
	assert.NotContains(t, m, "msg")
}

func TestNewJSONHandler_ExtendedLevels(t *testing.T) {
	tests := []struct {
		level    slog.Level
		expected string
	}{
		{LevelTrace, "TRACE"},
		{LevelDebug, "DEBUG"},
		{LevelInfo, "INFO"},
		{LevelWarn, "WARN"},
		{LevelError, "ERROR"},
		{LevelFatal, "FATAL"},
		{LevelPanic, "PANIC"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			var buf bytes.Buffer
			h := NewJSONHandler(&buf, &slog.HandlerOptions{Level: LevelTrace})
			logger := slog.New(h)

			logger.Log(context.Background(), tt.level, "test level")

			var m map[string]any
			require.NoError(t, json.Unmarshal(buf.Bytes(), &m))
			assert.Equal(t, tt.expected, m["level"])
			assert.Equal(t, "test level", m["message"])
		})
	}
}

func TestGCPReplaceAttr(t *testing.T) {
	var buf bytes.Buffer
	h := slog.NewJSONHandler(&buf, &slog.HandlerOptions{
		ReplaceAttr: GCPReplaceAttr,
	})
	logger := slog.New(h)

	logger.Warn("gcp alert")

	var m map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &m))
	assert.Equal(t, "WARNING", m["severity"])
	assert.Equal(t, "gcp alert", m["message"])
}
