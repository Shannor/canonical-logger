package clog

import (
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
)

func TestLevelSampler(t *testing.T) {
	tests := []struct {
		name       string
		sampleRate float64
		level      zerolog.Level
		expected   bool
	}{
		{"Always allow Warn", 0.0, zerolog.WarnLevel, true},
		{"Always allow Error", 0.0, zerolog.ErrorLevel, true},
		{"Always allow Fatal", 0.0, zerolog.FatalLevel, true},
		{"Always allow Panic", 0.0, zerolog.PanicLevel, true},
		{"Sampling 100% Info", 1.0, zerolog.InfoLevel, true},
		{"Sampling 0% Info", 0.0, zerolog.InfoLevel, false},
		{"Sampling 0% Debug", 0.0, zerolog.DebugLevel, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := LevelSampler{SampleRate: tt.sampleRate}
			assert.Equal(t, tt.expected, s.Sample(tt.level))
		})
	}
}

func TestConfigureGCPLogLevels(t *testing.T) {
	origWarn := zerolog.LevelWarnValue
	origError := zerolog.LevelErrorValue
	origDebug := zerolog.LevelDebugValue
	origInfo := zerolog.LevelInfoValue
	origFatal := zerolog.LevelFatalValue
	origPanic := zerolog.LevelPanicValue
	t.Cleanup(func() {
		zerolog.LevelWarnValue = origWarn
		zerolog.LevelErrorValue = origError
		zerolog.LevelDebugValue = origDebug
		zerolog.LevelInfoValue = origInfo
		zerolog.LevelFatalValue = origFatal
		zerolog.LevelPanicValue = origPanic
	})

	ConfigureGCPLogLevels()

	assert.Equal(t, "DEBUG", zerolog.LevelDebugValue)
	assert.Equal(t, "INFO", zerolog.LevelInfoValue)
	assert.Equal(t, "WARNING", zerolog.LevelWarnValue)
	assert.Equal(t, "ERROR", zerolog.LevelErrorValue)
	assert.Equal(t, "CRITICAL", zerolog.LevelFatalValue)
	assert.Equal(t, "EMERGENCY", zerolog.LevelPanicValue)
}
