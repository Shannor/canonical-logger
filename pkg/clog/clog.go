// Package clog provides canonical log line helpers and middlewares.
// This package is an alias for the root package github.com/Shannor/canonical-logger.
package clog

import (
	"context"

	root "github.com/Shannor/canonical-logger"
)

// Fields is a convenience alias for map[string]any.
type Fields = root.Fields

// Groups is a convenience alias for map[string]any.
type Groups = root.Groups

// Root is a convenience alias for map[string]any.
type Root = root.Root

// FieldBag is a thread-safe accumulator for structured log fields.
type FieldBag = root.FieldBag

// LevelSampler is a sampler that samples based on log level.
type LevelSampler = root.LevelSampler

// SampleEvaluator determines whether a log record should be sampled based on context and FieldBag.
type SampleEvaluator = root.SampleEvaluator

// SamplingHandler wraps an slog.Handler with level-based sampling.
type SamplingHandler = root.SamplingHandler

// ConsoleHandler is a human-readable, optionally colorized slog.Handler for local development.
type ConsoleHandler = root.ConsoleHandler

// ConsoleHandlerOptions configures ConsoleHandler behavior.
type ConsoleHandlerOptions = root.ConsoleHandlerOptions

// ContextResolver extracts a *FieldBag from a framework-specific context.
type ContextResolver = root.ContextResolver

// ErrorFormatter extracts structured metadata fields from an error for the "error" group.
type ErrorFormatter = root.ErrorFormatter

// HTTPHandlerFunc is an error-returning HTTP handler used with HTTPHandler.
type HTTPHandlerFunc = root.HTTPHandlerFunc

const (
	LevelTrace = root.LevelTrace
	LevelDebug = root.LevelDebug
	LevelInfo  = root.LevelInfo
	LevelWarn  = root.LevelWarn
	LevelError = root.LevelError
	LevelFatal = root.LevelFatal
	LevelPanic = root.LevelPanic
)

var (
	// Context management & field helpers
	NewContext              = root.NewContext
	NewContextWithLogger    = root.NewContextWithLogger
	LoggerFromContext       = root.LoggerFromContext
	BagFromContext          = root.BagFromContext
	RegisterContextResolver = root.RegisterContextResolver
	WithField               = root.WithField
	WithFields              = root.WithFields
	WithAttrs               = root.WithAttrs
	WithGroupField          = root.WithGroupField
	WithGroupFields         = root.WithGroupFields
	WithGroupAttrs          = root.WithGroupAttrs
	WithMessage             = root.WithMessage

	// Result tracking & error formatting
	Run                   = root.Run
	HTTPHandler           = root.HTTPHandler
	SetErrorFormatter     = root.SetErrorFormatter
	DefaultErrorFormatter = root.DefaultErrorFormatter

	// Background and asynchronous tasks
	NewBackground = root.NewBackground
	RunFunc       = root.RunFunc
	Go            = root.Go

	// Middlewares & Trace Extraction
	HTTPMiddleware      = root.HTTPMiddleware
	HTTPTraceMiddleware = root.HTTPTraceMiddleware
	ExtractTrace        = root.ExtractTrace

	// Handlers & Helpers
	NewSamplingHandler              = root.NewSamplingHandler
	NewSamplingHandlerWithEvaluator = root.NewSamplingHandlerWithEvaluator
	NewJSONHandler                  = root.NewJSONHandler
	NewConsoleHandler               = root.NewConsoleHandler
	GCPReplaceAttr                  = root.GCPReplaceAttr
)

// RunValue is the value-returning form of Run. See root.RunValue.
func RunValue[T any](ctx context.Context, baseEvent string, fn func(ctx context.Context) (T, error)) (T, error) {
	return root.RunValue(ctx, baseEvent, fn)
}
