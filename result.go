package xlog

import "context"

// SetResult sets the canonical log line event name with a -success or -failure suffix based on err.
// Use at boundary layers: API handlers, background jobs, cron jobs.
// If err is non-nil and no "error" group exists yet, creates one with err.Error() as the "err" field.
func SetResult(ctx context.Context, baseEvent string, err error) context.Context {
	bag := BagFromContext(ctx)
	if bag == nil {
		return ctx
	}
	if err != nil {
		if !bag.hasGroup("error") {
			bag.setGroup("error", "err", err.Error())
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
//		defer xlog.DeferResult(ctx, "get-campaign", &err)
//		...
//	}
func DeferResult(ctx context.Context, baseEvent string, err *error) {
	var e error
	if err != nil {
		e = *err
	}
	SetResult(ctx, baseEvent, e)
}
