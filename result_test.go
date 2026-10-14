package clog

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSetResult_NilError verifies that SetResult sets message to "{baseEvent}-success" when err is nil.
func TestSetResult_NilError(t *testing.T) {
	ctx := NewContext(context.Background())
	SetResult(ctx, "get-campaign", nil)

	bag := BagFromContext(ctx)
	require.NotNil(t, bag)
	assert.Equal(t, "get-campaign-success", bag.Message())
}

// TestSetResult_WithError verifies that SetResult sets message to "{baseEvent}-failure" when err is non-nil.
func TestSetResult_WithError(t *testing.T) {
	ctx := NewContext(context.Background())
	SetResult(ctx, "get-campaign", errors.New("something went wrong"))

	bag := BagFromContext(ctx)
	require.NotNil(t, bag)
	assert.Equal(t, "get-campaign-failure", bag.Message())
}

// TestSetResult_WithError_SetsErrorGroup verifies that SetResult creates an error group when none exists.
func TestSetResult_WithError_SetsErrorGroup(t *testing.T) {
	ctx := NewContext(context.Background())
	SetResult(ctx, "get-campaign", errors.New("db error"))

	bag := BagFromContext(ctx)
	require.NotNil(t, bag)
	assert.True(t, bag.hasGroup("error"))

	errGroup := bag.getGroup("error")
	require.NotNil(t, errGroup)
	assert.Equal(t, "db error", errGroup["err"])
}

// TestSetResult_NilBag verifies that SetResult handles nil bag gracefully (no panic).
func TestSetResult_NilBag(t *testing.T) {
	ctx := context.Background() // no FieldBag in context
	result := SetResult(ctx, "get-campaign", nil)
	assert.Equal(t, ctx, result)
}

// TestSetResult_ReturnsCtx verifies that SetResult returns the context.
func TestSetResult_ReturnsCtx(t *testing.T) {
	ctx := NewContext(context.Background())
	returned := SetResult(ctx, "get-campaign", nil)
	assert.Equal(t, ctx, returned)
}

// TestDeferResult_Success verifies that DeferResult logs success when error is nil at return time.
func TestDeferResult_Success(t *testing.T) {
	ctx := NewContext(context.Background())
	func() {
		var err error
		defer DeferResult(ctx, "my-operation", &err)
	}()

	bag := BagFromContext(ctx)
	require.NotNil(t, bag)
	assert.Equal(t, "my-operation-success", bag.Message())
	assert.False(t, bag.hasGroup("error"))
}

// TestDeferResult_Failure verifies that DeferResult logs failure when error is set.
func TestDeferResult_Failure(t *testing.T) {
	ctx := NewContext(context.Background())
	func() {
		var err error
		defer DeferResult(ctx, "my-operation", &err)
		err = errors.New("something went wrong")
	}()

	bag := BagFromContext(ctx)
	require.NotNil(t, bag)
	assert.Equal(t, "my-operation-failure", bag.Message())
	assert.True(t, bag.hasGroup("error"))
	errGroup := bag.getGroup("error")
	require.NotNil(t, errGroup)
	assert.Equal(t, "something went wrong", errGroup["err"])
}

// TestDeferResult_NilPointer verifies that DeferResult handles nil error pointer gracefully.
func TestDeferResult_NilPointer(t *testing.T) {
	ctx := NewContext(context.Background())
	func() {
		defer DeferResult(ctx, "my-operation", nil)
	}()

	bag := BagFromContext(ctx)
	require.NotNil(t, bag)
	assert.Equal(t, "my-operation-success", bag.Message())
}

// TestDeferResult_ShadowedError_FailsToPropagate demonstrates that shadowing an error
// variable in a nested block (using :=) prevents the deferred function from seeing the error.
func TestDeferResult_ShadowedError_FailsToPropagate(t *testing.T) {
	ctx := NewContext(context.Background())
	func() {
		var err error
		defer DeferResult(ctx, "my-operation", &err)

		if true {
			err := errors.New("nested block error")
			_ = err
		}
	}()

	bag := BagFromContext(ctx)
	require.NotNil(t, bag)
	assert.Equal(t, "my-operation-success", bag.Message())
	assert.False(t, bag.hasGroup("error"))
}

// TestDeferResult_ReassignedError_Propagates demonstrates that reassigning the outer error
// variable (using =) inside a nested block correctly propagates the error to the deferred function.
func TestDeferResult_ReassignedError_Propagates(t *testing.T) {
	ctx := NewContext(context.Background())
	func() {
		var err error
		defer DeferResult(ctx, "my-operation", &err)

		if true {
			err = errors.New("nested block error")
		}
	}()

	bag := BagFromContext(ctx)
	require.NotNil(t, bag)
	assert.Equal(t, "my-operation-failure", bag.Message())
	assert.True(t, bag.hasGroup("error"))
	errGroup := bag.getGroup("error")
	require.NotNil(t, errGroup)
	assert.Equal(t, "nested block error", errGroup["err"])
}

// TestDeferResult_SameScope_AssignWithNewVariable_Propagates demonstrates that using := in the same
// scope as the outer error variable declaration, where at least one new variable is being declared,
// correctly reuses/reassigns the existing err variable and propagates the error.
func TestDeferResult_SameScope_AssignWithNewVariable_Propagates(t *testing.T) {
	ctx := NewContext(context.Background())
	dummyFunc := func() (string, error) {
		return "", errors.New("same scope error")
	}

	func() {
		var err error
		defer DeferResult(ctx, "my-operation", &err)

		val, err := dummyFunc()
		_ = val
		_ = err
	}()

	bag := BagFromContext(ctx)
	require.NotNil(t, bag)
	assert.Equal(t, "my-operation-failure", bag.Message())
	assert.True(t, bag.hasGroup("error"))
	errGroup := bag.getGroup("error")
	require.NotNil(t, errGroup)
	assert.Equal(t, "same scope error", errGroup["err"])
}

type codedError struct {
	msg      string
	code     string
	category string
	fields   map[string]any
	cause    error
}

func (e *codedError) Error() string                { return e.msg }
func (e *codedError) Code() string                 { return e.code }
func (e *codedError) Category() string             { return e.category }
func (e *codedError) ErrorFields() map[string]any  { return e.fields }
func (e *codedError) Unwrap() error                { return e.cause }

func TestDefaultErrorFormatter_ExtractsMetadataAndCause(t *testing.T) {
	rootErr := errors.New("connection refused")
	richErr := &codedError{
		msg:      "orderDAO.Insert: failed to connect",
		code:     "DB_CONN_ERR",
		category: "transient",
		fields:   map[string]any{"retryable": true},
		cause:    rootErr,
	}

	ctx := NewContext(context.Background())
	SetResult(ctx, "create-order", richErr)

	bag := BagFromContext(ctx)
	errGroup := bag.getGroup("error")
	require.NotNil(t, errGroup)
	assert.Equal(t, "orderDAO.Insert: failed to connect", errGroup["err"])
	assert.Equal(t, "DB_CONN_ERR", errGroup["code"])
	assert.Equal(t, "transient", errGroup["category"])
	assert.Equal(t, true, errGroup["retryable"])
	assert.Equal(t, "connection refused", errGroup["cause"])
}

func TestSetErrorFormatter_CustomAndReset(t *testing.T) {
	t.Cleanup(func() { SetErrorFormatter(nil) })

	SetErrorFormatter(func(err error) Fields {
		return Fields{
			"err":    err.Error(),
			"custom": "formatted",
		}
	})

	ctx := NewContext(context.Background())
	SetResult(ctx, "custom-op", errors.New("boom"))

	bag := BagFromContext(ctx)
	errGroup := bag.getGroup("error")
	require.NotNil(t, errGroup)
	assert.Equal(t, "boom", errGroup["err"])
	assert.Equal(t, "formatted", errGroup["custom"])

	// Reset to DefaultErrorFormatter
	SetErrorFormatter(nil)
	ctx2 := NewContext(context.Background())
	SetResult(ctx2, "default-op", errors.New("plain"))
	errGroup2 := BagFromContext(ctx2).getGroup("error")
	assert.Equal(t, "plain", errGroup2["err"])
	assert.NotContains(t, errGroup2, "custom")
}
