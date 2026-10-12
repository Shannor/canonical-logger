package clog

import (
	"bytes"
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
		{"Always allow Warn", 0.0, slog.LevelWarn, true},
		{"Always allow Error", 0.0, slog.LevelError, true},
		{"Always allow high Error", 0.0, slog.Level(12), true},
		{"Sampling 100% Info", 1.0, slog.LevelInfo, true},
		{"Sampling 0% Info", 0.0, slog.LevelInfo, false},
		{"Sampling 0% Debug", 0.0, slog.LevelDebug, false},
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
