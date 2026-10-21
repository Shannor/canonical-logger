package clog_test

import (
	"context"
	"testing"

	"github.com/Shannor/canonical-logger/pkg/clog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPkgClog_Forwarding(t *testing.T) {
	ctx := context.Background()
	ctx = clog.NewContext(ctx)

	bag := clog.BagFromContext(ctx)
	require.NotNil(t, bag)

	ctx = clog.WithField(ctx, "k1", "v1")
	ctx = clog.WithFields(ctx, clog.Fields{"k2": "v2"})
	ctx = clog.WithGroupField(ctx, "service.method", "action", "fetch")
	ctx = clog.WithGroupFields(ctx, "service.method", clog.Fields{"count": 5})
	ctx = clog.WithMessage(ctx, "custom-msg")

	assert.Equal(t, "custom-msg", bag.Message())

	require.NoError(t, clog.Run(ctx, "task", func(context.Context) error { return nil }))
	assert.Equal(t, "task-success", bag.Message())

	v, err := clog.RunValue(ctx, "task", func(context.Context) (int, error) { return 7, nil })
	require.NoError(t, err)
	assert.Equal(t, 7, v)
}
