// Package clog provides canonical log line helpers and middlewares.
// This package is an alias for the root package github.com/Shannor/canonical-logger.
package clog

import (
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

// GCPWriter implements io.Writer and sends logs to Google Cloud Logging.
type GCPWriter = root.GCPWriter

// GinMiddlewareOption allows configuring GinMiddleware behavior.
type GinMiddlewareOption = root.GinMiddlewareOption

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
	NewContext           = root.NewContext
	NewContextWithLogger = root.NewContextWithLogger
	LoggerFromContext    = root.LoggerFromContext
	BagFromContext       = root.BagFromContext
	WithField            = root.WithField
	WithFields           = root.WithFields
	WithGroupField       = root.WithGroupField
	WithGroupFields      = root.WithGroupFields
	WithMessage          = root.WithMessage

	// Result tracking
	SetResult   = root.SetResult
	DeferResult = root.DeferResult

	// Background and asynchronous tasks
	NewBackground = root.NewBackground
	Func          = root.Func
	CronFunc      = root.CronFunc
	Go            = root.Go

	// Middlewares
	GinMiddleware       = root.GinMiddleware
	WithSensitiveParams = root.WithSensitiveParams
	HTTPMiddleware      = root.HTTPMiddleware
	TraceID             = root.TraceID
	HTTPTraceMiddleware = root.HTTPTraceMiddleware

	// Handlers & Helpers
	NewSamplingHandler              = root.NewSamplingHandler
	NewSamplingHandlerWithEvaluator = root.NewSamplingHandlerWithEvaluator
	NewJSONHandler                  = root.NewJSONHandler
	GCPReplaceAttr                  = root.GCPReplaceAttr
)
