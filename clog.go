package clog

import (
	"context"
	"io"
	"log/slog"
	"sync/atomic"
)

// LevelSampler determines whether a log record should be sampled based on log level and sample rate.
// Warnings, Errors, and higher levels are always retained (100%).
// Info, Debug, and lower levels are sampled at SampleRate (0.0 to 1.0).
type LevelSampler struct {
	// SampleRate is the probability of a log being sampled (0.0 to 1.0).
	// Applied only to Info and Debug levels.
	SampleRate float64
	counter    uint64
}

// Sample reports whether a record at lvl should be emitted.
func (s *LevelSampler) Sample(lvl slog.Level) bool {
	if lvl >= slog.LevelWarn {
		return true // Always capture Warn and Error
	}
	if s.SampleRate >= 1.0 {
		return true
	}
	if s.SampleRate <= 0.0 {
		return false
	}
	n := uint64(1.0 / s.SampleRate)
	if n == 0 {
		n = 1
	}
	return (atomic.AddUint64(&s.counter, 1) % n) == 0
}

// SamplingHandler wraps an slog.Handler with level-based sampling.
// Warn and Error levels are always emitted. Info and Debug levels are sampled based on sampleRate.
type SamplingHandler struct {
	handler slog.Handler
	sampler LevelSampler
}

// NewSamplingHandler creates a SamplingHandler wrapping the given slog.Handler.
func NewSamplingHandler(h slog.Handler, sampleRate float64) *SamplingHandler {
	return &SamplingHandler{
		handler: h,
		sampler: LevelSampler{SampleRate: sampleRate},
	}
}

// Enabled implements slog.Handler.Enabled, applying level sampling.
func (h *SamplingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	if !h.sampler.Sample(level) {
		return false
	}
	return h.handler.Enabled(ctx, level)
}

// Handle implements slog.Handler.Handle.
func (h *SamplingHandler) Handle(ctx context.Context, r slog.Record) error {
	return h.handler.Handle(ctx, r)
}

// WithAttrs implements slog.Handler.WithAttrs.
func (h *SamplingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &SamplingHandler{
		handler: h.handler.WithAttrs(attrs),
		sampler: h.sampler,
	}
}

// WithGroup implements slog.Handler.WithGroup.
func (h *SamplingHandler) WithGroup(name string) slog.Handler {
	return &SamplingHandler{
		handler: h.handler.WithGroup(name),
		sampler: h.sampler,
	}
}

// NewJSONHandler creates an slog.JSONHandler with canonical logger standard options,
// mapping the default slog message key ("msg") to "message" for consistency with
// standard JSON logging in Cloud Logging, Datadog, and Kubernetes.
func NewJSONHandler(w io.Writer, opts *slog.HandlerOptions) *slog.JSONHandler {
	if opts == nil {
		opts = &slog.HandlerOptions{}
	}
	userReplace := opts.ReplaceAttr
	optsCopy := *opts
	optsCopy.ReplaceAttr = func(groups []string, a slog.Attr) slog.Attr {
		if len(groups) == 0 && a.Key == slog.MessageKey {
			a.Key = "message"
		}
		if userReplace != nil {
			return userReplace(groups, a)
		}
		return a
	}
	return slog.NewJSONHandler(w, &optsCopy)
}

// GCPReplaceAttr maps slog.Level keys to Google Cloud Logging "severity" strings
// ("DEBUG", "INFO", "WARNING", "ERROR") and the message key to "message".
// Pass this to slog.HandlerOptions.ReplaceAttr when streaming logs to GCP.
func GCPReplaceAttr(groups []string, a slog.Attr) slog.Attr {
	if len(groups) == 0 {
		switch a.Key {
		case slog.LevelKey:
			level, ok := a.Value.Any().(slog.Level)
			if !ok {
				return a
			}
			var sev string
			switch {
			case level >= slog.LevelError:
				sev = "ERROR"
			case level >= slog.LevelWarn:
				sev = "WARNING"
			case level >= slog.LevelInfo:
				sev = "INFO"
			default:
				sev = "DEBUG"
			}
			return slog.String("severity", sev)
		case slog.MessageKey:
			return slog.String("message", a.Value.String())
		}
	}
	return a
}
