package clog

import (
	"context"
	"log/slog"
	"maps"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	ginmiddleware "github.com/oapi-codegen/gin-middleware"
)

// Fields is a convenience alias for map[string]any.
// Fields are the leaf properties of a log line or group.
type Fields = map[string]any

// Groups is a convenience alias for map[string]any.
// Groups represent named sections of the log that are emitted
// as nested JSON objects on the root canonical log line.
type Groups = map[string]any

// Root is a convenience alias for map[string]any.
// Used for the root level of the entire canonical log body.
type Root = map[string]any

type fieldBagCtxKey struct{}
type loggerCtxKey struct{}

const defaultCanonicalMessage = "canonical-log-line"

// ginFieldBagKey is the gin.Context key used to store the FieldBag so it survives
// any middleware (e.g. auth middleware) that replaces c.Request with a fresh context.
const ginFieldBagKey = "__clog_fieldbag__"

// maxGroupDepth is the maximum nesting depth for dot-separated group paths.
const maxGroupDepth = 3

// FieldBag is a thread-safe accumulator for structured log fields.
// It supports root-level flat fields and named group fields that are
// emitted as nested JSON objects in the canonical log line.
// Groups support dot-separated paths (e.g. "db.getCampaignById") for nesting
// up to maxGroupDepth levels deep.
type FieldBag struct {
	mu      sync.Mutex
	root    Root
	groups  Groups
	message string
	logger  *slog.Logger
}

func (b *FieldBag) setLogger(l *slog.Logger) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.logger = l
}

func (b *FieldBag) getLogger() *slog.Logger {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.logger
}

// ensurePath navigates or creates nested maps for a dot-separated group path.
// Returns the deepest map. Caller must hold b.mu.
func (b *FieldBag) ensurePath(path string) Groups {
	if b.groups == nil {
		b.groups = make(Groups)
	}

	parts := strings.Split(path, ".")
	if len(parts) > maxGroupDepth {
		parts = parts[:maxGroupDepth]
	}

	current := b.groups
	for _, part := range parts {
		if part == "" {
			continue
		}
		if existing, ok := current[part]; ok {
			if m, ok := existing.(Groups); ok {
				current = m
			} else {
				next := make(Groups)
				current[part] = next
				current = next
			}
		} else {
			next := make(Groups)
			current[part] = next
			current = next
		}
	}
	return current
}

// deepCopyMap recursively copies a nested map structure.
func deepCopyMap(src Root) Root {
	out := make(Root, len(src))
	for k, v := range src {
		if m, ok := v.(Root); ok {
			out[k] = deepCopyMap(m)
		} else {
			out[k] = v
		}
	}
	return out
}

// toSlogAttr converts a key-value pair into a typed slog.Attr without reflection
// for common types, nested maps, slog.Attr/Value/LogValuer, and errors.
func toSlogAttr(key string, v any) slog.Attr {
	switch val := v.(type) {
	case string:
		return slog.String(key, val)
	case int:
		return slog.Int(key, val)
	case int64:
		return slog.Int64(key, val)
	case int32:
		return slog.Int64(key, int64(val))
	case uint:
		return slog.Uint64(key, uint64(val))
	case uint64:
		return slog.Uint64(key, val)
	case uint32:
		return slog.Uint64(key, uint64(val))
	case float64:
		return slog.Float64(key, val)
	case float32:
		return slog.Float64(key, float64(val))
	case bool:
		return slog.Bool(key, val)
	case time.Duration:
		return slog.Duration(key, val)
	case time.Time:
		return slog.Time(key, val)
	case map[string]any:
		return buildGroupAttr(key, val)
	case slog.Attr:
		if val.Key == "" {
			val.Key = key
		}
		return val
	case slog.Value:
		return slog.Attr{Key: key, Value: val}
	case slog.LogValuer:
		return slog.Attr{Key: key, Value: val.LogValue()}
	case error:
		return slog.String(key, val.Error())
	default:
		return slog.Any(key, v)
	}
}

// buildGroupAttr recursively constructs a nested slog.Attr group from a map.
func buildGroupAttr(key string, m Groups) slog.Attr {
	attrs := make([]slog.Attr, 0, len(m))
	for k, v := range m {
		attrs = append(attrs, toSlogAttr(k, v))
	}
	return slog.GroupAttrs(key, attrs...)
}

// Set stores a root-level field.
func (b *FieldBag) Set(key string, value any) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.root == nil {
		b.root = make(Root)
	}
	b.root[key] = value
}

// SetMany stores multiple root-level fields.
func (b *FieldBag) SetMany(fields Fields) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.root == nil {
		b.root = make(Root, len(fields))
	}
	maps.Copy(b.root, fields)
}

// setGroup stores a single field within a named group.
// The group parameter supports dot-separated paths (e.g. "db.getCampaignById")
// for nesting up to 3 levels deep.
func (b *FieldBag) setGroup(group, key string, value any) {
	b.mu.Lock()
	defer b.mu.Unlock()
	target := b.ensurePath(group)
	target[key] = value
}

// setGroupMany stores multiple fields within a named group.
// The group parameter supports dot-separated paths (e.g. "db.getCampaignById")
// for nesting up to 3 levels deep.
func (b *FieldBag) setGroupMany(group string, fields Fields) {
	b.mu.Lock()
	defer b.mu.Unlock()
	target := b.ensurePath(group)
	maps.Copy(target, fields)
}

// setMessage overrides the canonical log line message string.
func (b *FieldBag) setMessage(msg string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.message = msg
}

// Message returns the message for the canonical log line.
// Falls back to "canonical-log-line" if never set.
func (b *FieldBag) Message() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.message == "" {
		return defaultCanonicalMessage
	}
	return b.message
}

// Attrs returns all accumulated root fields and nested groups as slog.Attrs.
// It is safe to call on a nil *FieldBag (returns nil).
func (b *FieldBag) Attrs() []slog.Attr {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	attrs := make([]slog.Attr, 0, len(b.root)+len(b.groups))
	for k, v := range b.root {
		attrs = append(attrs, toSlogAttr(k, v))
	}

	for groupName, groupValue := range b.groups {
		attrs = append(attrs, toSlogAttr(groupName, groupValue))
	}

	return attrs
}

// NewContext creates a new FieldBag, stores it in the context, and returns the updated context.
// If ctx already carries a custom *slog.Logger, the new FieldBag inherits it.
func NewContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, fieldBagCtxKey{}, &FieldBag{
		logger: rawLoggerFromContext(ctx),
	})
}

// NewContextWithLogger returns a new context carrying the given *slog.Logger.
// If a FieldBag is present in ctx, its logger is also updated so canonical flushes
// use the updated logger even when callers do not reassign the outer request/task context.
func NewContextWithLogger(ctx context.Context, logger *slog.Logger) context.Context {
	if bag := BagFromContext(ctx); bag != nil {
		bag.setLogger(logger)
	}
	return context.WithValue(ctx, loggerCtxKey{}, logger)
}

func rawLoggerFromContext(ctx context.Context) *slog.Logger {
	if ctx == nil {
		return nil
	}
	if l, ok := ctx.Value(loggerCtxKey{}).(*slog.Logger); ok && l != nil {
		if bag := BagFromContext(ctx); bag != nil {
			if bagLogger := bag.getLogger(); bagLogger != nil {
				return bagLogger
			}
		}
		return l
	}
	if bag := BagFromContext(ctx); bag != nil {
		if l := bag.getLogger(); l != nil {
			return l
		}
	}
	return nil
}

// LoggerFromContext retrieves the *slog.Logger from the context, or returns slog.Default() if none is set.
func LoggerFromContext(ctx context.Context) *slog.Logger {
	if l := rawLoggerFromContext(ctx); l != nil {
		return l
	}
	return slog.Default()
}

// BagFromContext retrieves the FieldBag from the context. Returns nil if not present.
// It first checks the standard context chain, then falls back to the gin.Context store
// (mirroring the context extraction pattern) so it survives any middleware
// that replaces c.Request with a fresh context.
func BagFromContext(ctx context.Context) *FieldBag {
	if ctx == nil {
		return nil
	}

	if bag, ok := ctx.Value(fieldBagCtxKey{}).(*FieldBag); ok && bag != nil {
		return bag
	}

	// Check standard gin.Context if present
	if ginCtx, ok := ctx.Value(gin.ContextKey).(*gin.Context); ok && ginCtx != nil {
		if val, exists := ginCtx.Get(ginFieldBagKey); exists {
			if bag, ok := val.(*FieldBag); ok {
				return bag
			}
		}
	}

	// Next, check the gin openapi context if present
	if ginCtx := ginmiddleware.GetGinContext(ctx); ginCtx != nil {
		if val, exists := ginCtx.Get(ginFieldBagKey); exists {
			if bag, ok := val.(*FieldBag); ok {
				return bag
			}
		}
	}

	return nil
}

// WithField adds a root-level field to the FieldBag in the context.
// No-op if no FieldBag is present.
func WithField(ctx context.Context, key string, value any) context.Context {
	bag := BagFromContext(ctx)
	if bag == nil {
		return ctx
	}
	bag.Set(key, value)
	return ctx
}

// WithFields adds multiple root-level fields to the FieldBag in the context.
// No-op if no FieldBag is present.
func WithFields(ctx context.Context, fields Fields) context.Context {
	bag := BagFromContext(ctx)
	if bag == nil {
		return ctx
	}
	bag.SetMany(fields)
	return ctx
}

// WithAttrs adds strongly-typed slog.Attr values as root-level fields to the FieldBag in the context.
// No-op if no FieldBag is present.
func WithAttrs(ctx context.Context, attrs ...slog.Attr) context.Context {
	bag := BagFromContext(ctx)
	if bag == nil || len(attrs) == 0 {
		return ctx
	}
	bag.mu.Lock()
	defer bag.mu.Unlock()
	if bag.root == nil {
		bag.root = make(Root, len(attrs))
	}
	for _, a := range attrs {
		if a.Key != "" {
			bag.root[a.Key] = a
		}
	}
	return ctx
}

// WithGroupField adds a single field to a named group in the FieldBag in the context.
// No-op if no FieldBag is present.
func WithGroupField(ctx context.Context, group, key string, value any) context.Context {
	bag := BagFromContext(ctx)
	if bag == nil {
		return ctx
	}
	bag.setGroup(group, key, value)
	return ctx
}

// WithGroupFields adds multiple fields to a named group in the FieldBag in the context.
// No-op if no FieldBag is present.
func WithGroupFields(ctx context.Context, group string, fields Fields) context.Context {
	bag := BagFromContext(ctx)
	if bag == nil {
		return ctx
	}
	bag.setGroupMany(group, fields)
	return ctx
}

// WithGroupAttrs adds strongly-typed slog.Attr values to a named group in the FieldBag in the context.
// No-op if no FieldBag is present.
func WithGroupAttrs(ctx context.Context, group string, attrs ...slog.Attr) context.Context {
	bag := BagFromContext(ctx)
	if bag == nil || len(attrs) == 0 {
		return ctx
	}
	bag.mu.Lock()
	defer bag.mu.Unlock()
	target := bag.ensurePath(group)
	for _, a := range attrs {
		if a.Key != "" {
			target[a.Key] = a
		}
	}
	return ctx
}

// WithMessage overrides the canonical log line message string in the context's FieldBag.
// No-op if no FieldBag is present.
func WithMessage(ctx context.Context, msg string) context.Context {
	bag := BagFromContext(ctx)
	if bag == nil {
		return ctx
	}
	bag.setMessage(msg)
	return ctx
}

// getGroup returns a deep copy of the named group's fields, or nil if not present.
// Supports dot-separated paths (e.g. "db.getCampaignById").
// Safe to call on a nil *FieldBag (returns nil).
func (b *FieldBag) getGroup(name string) Groups {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	parts := strings.Split(name, ".")
	current := b.groups
	for _, part := range parts {
		if part == "" {
			continue
		}
		v, ok := current[part]
		if !ok {
			return nil
		}
		m, ok := v.(Groups)
		if !ok {
			return nil
		}
		current = m
	}
	return deepCopyMap(current)
}

// snapshot returns a deep copy of all root fields and all groups as a single map.
// Groups fields are stored as nested map[string]any under their group name.
// Safe to call on a nil *FieldBag (returns nil).
func (b *FieldBag) snapshot() Root {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make(Root)
	maps.Copy(out, b.root)
	for groupName, groupValue := range b.groups {
		if m, ok := groupValue.(Groups); ok {
			out[groupName] = deepCopyMap(m)
		} else {
			out[groupName] = groupValue
		}
	}
	return out
}

// hasGroup returns true if the named group path exists.
// Supports dot-separated paths (e.g. "db.getCampaignById").
// Safe to call on a nil *FieldBag (returns false).
func (b *FieldBag) hasGroup(name string) bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	parts := strings.Split(name, ".")
	current := b.groups
	for _, part := range parts {
		if part == "" {
			continue
		}
		v, ok := current[part]
		if !ok {
			return false
		}
		if m, ok := v.(Groups); ok {
			current = m
		} else {
			return true
		}
	}
	return true
}

const (
	warning  = "warning"
	warnings = "warnings"
)

// hasWarning returns true if there is a root-level "warning(s)" field,
// if a group is named "warning(s)", or if any group contains a key named "warning(s)".
// Safe to call on a nil *FieldBag (returns false).
func (b *FieldBag) hasWarning() bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	if _, ok := b.root[warning]; ok {
		return true
	}
	if _, ok := b.root[warnings]; ok {
		return true
	}
	return hasWarningInMap(b.groups)
}

func hasWarningInMap(m Fields) bool {
	for k, v := range m {
		if k == warning || k == warnings {
			return true
		}
		if subMap, ok := v.(Fields); ok {
			if hasWarningInMap(subMap) {
				return true
			}
		}
	}
	return false
}
