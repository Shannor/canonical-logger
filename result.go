package clog

import (
	"context"
	"errors"
	"maps"
	"sync"
)

// ErrorFormatter extracts structured metadata fields from an error for the "error" group.
type ErrorFormatter func(err error) Fields

var (
	errorFormatterMu sync.RWMutex
	errorFormatter   ErrorFormatter = DefaultErrorFormatter
)

// SetErrorFormatter configures a custom ErrorFormatter used by SetResult and DeferResult.
// Passing nil restores DefaultErrorFormatter.
func SetErrorFormatter(fn ErrorFormatter) {
	errorFormatterMu.Lock()
	defer errorFormatterMu.Unlock()
	if fn == nil {
		errorFormatter = DefaultErrorFormatter
		return
	}
	errorFormatter = fn
}

func getErrorFormatter() ErrorFormatter {
	errorFormatterMu.RLock()
	defer errorFormatterMu.RUnlock()
	return errorFormatter
}

// DefaultErrorFormatter extracts the error message ("err"), root cause ("cause" when wrapped),
// and optional Code(), Category(), and ErrorFields() metadata from the error chain.
func DefaultErrorFormatter(err error) Fields {
	if err == nil {
		return nil
	}
	fields := Fields{
		"err": err.Error(),
	}

	var fErr interface{ ErrorFields() map[string]any }
	if errors.As(err, &fErr) {
		if extra := fErr.ErrorFields(); len(extra) > 0 {
			maps.Copy(fields, extra)
		}
	}

	var codeErr interface{ Code() string }
	if errors.As(err, &codeErr) {
		if code := codeErr.Code(); code != "" {
			fields["code"] = code
		}
	}

	var catErr interface{ Category() string }
	if errors.As(err, &catErr) {
		if cat := catErr.Category(); cat != "" {
			fields["category"] = cat
		}
	}

	if cause := unwrapRootCause(err); cause != nil && cause != err {
		fields["cause"] = cause.Error()
	}

	return fields
}

func unwrapRootCause(err error) error {
	curr := err
	for {
		unwrapped := errors.Unwrap(curr)
		if unwrapped == nil {
			return curr
		}
		curr = unwrapped
	}
}

// SetResult sets the canonical log line event name with a -success or -failure suffix based on err.
// Use at boundary layers: API handlers, background jobs, cron jobs.
// If err is non-nil and no "error" group exists yet, populates the "error" group using the active ErrorFormatter.
func SetResult(ctx context.Context, baseEvent string, err error) context.Context {
	bag := BagFromContext(ctx)
	if bag == nil {
		return ctx
	}
	if err != nil {
		if !bag.hasGroup("error") {
			formatter := getErrorFormatter()
			if fields := formatter(err); len(fields) > 0 {
				bag.setGroupMany("error", fields)
			} else {
				bag.setGroup("error", "err", err.Error())
			}
		}
		bag.setMessage(baseEvent + "-failure")
		return ctx
	}
	bag.setMessage(baseEvent + "-success")
	return ctx
}

// DeferResult is the defer-friendly form of SetResult. It reads *err at the time the
// deferred call runs (function return), not when the defer statement is registered, so it
// captures the final error value.
//
// Usage:
//
//	func (a API) GetCampaign(ctx context.Context, ...) (..., error) {
//		var err error
//		defer clog.DeferResult(ctx, "get-campaign", &err)
//		...
//	}
func DeferResult(ctx context.Context, baseEvent string, err *error) {
	var e error
	if err != nil {
		e = *err
	}
	SetResult(ctx, baseEvent, e)
}
