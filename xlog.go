package xlog

import (
	"github.com/rs/zerolog"
)

// LevelSampler is a sampler that samples based on log level.
type LevelSampler struct {
	// SampleRate is the probability of a log being sampled (0.0 to 1.0).
	// Applied only to Info, Debug, and Trace levels.
	SampleRate float64
}

// Sample implements the zerolog.Sampler interface.
func (s LevelSampler) Sample(lvl zerolog.Level) bool {
	if lvl >= zerolog.WarnLevel {
		return true // Always capture Warn, Error, Fatal, Panic
	}

	if s.SampleRate >= 1.0 {
		return true
	}

	if s.SampleRate <= 0.0 {
		return false
	}

	// Use zerolog's internal sampling logic
	bs := zerolog.BasicSampler{N: uint32(1.0 / s.SampleRate)}
	return bs.Sample(lvl)
}

// ConfigureGCPLogLevels sets Zerolog's level string values to match Google Cloud Logging severity names.
func ConfigureGCPLogLevels() {
	zerolog.LevelDebugValue = "DEBUG"
	zerolog.LevelInfoValue = "INFO"
	zerolog.LevelWarnValue = "WARNING"
	zerolog.LevelErrorValue = "ERROR"
	zerolog.LevelFatalValue = "CRITICAL"
	zerolog.LevelPanicValue = "EMERGENCY"
}
