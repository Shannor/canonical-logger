package xlog_test

import (
	"context"
	"testing"

	"github.com/Shannor/canonical-logger/pkg/xlog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPkgXlog_Forwarding(t *testing.T) {
	ctx := context.Background()
	ctx = xlog.NewContext(ctx)

	bag := xlog.BagFromContext(ctx)
	require.NotNil(t, bag)

	ctx = xlog.WithField(ctx, "k1", "v1")
	ctx = xlog.WithFields(ctx, xlog.Fields{"k2": "v2"})
	ctx = xlog.WithGroupField(ctx, "service.method", "action", "fetch")
	ctx = xlog.WithGroupFields(ctx, "service.method", xlog.Fields{"count": 5})
	ctx = xlog.WithMessage(ctx, "custom-msg")

	assert.Equal(t, "custom-msg", bag.Message())

	xlog.SetResult(ctx, "task", nil)
	assert.Equal(t, "task-success", bag.Message())
}
