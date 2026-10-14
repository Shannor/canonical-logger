package clog

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConsoleHandler_BasicAndCanonicalGroups(t *testing.T) {
	var buf bytes.Buffer
	h := NewConsoleHandler(&buf, &ConsoleHandlerOptions{
		Level:   LevelTrace,
		NoColor: true,
	})
	logger := slog.New(h)

	ctx := NewContext(context.Background())
	WithField(ctx, "status", 200)
	WithGroupFields(ctx, "actor", Fields{
		"requestorId": "usr_123",
	})
	WithGroupField(ctx, "db.queryOrders", "rows", 5)
	SetResult(ctx, "list-orders", nil)

	bag := BagFromContext(ctx)
	logger.LogAttrs(ctx, LevelInfo, bag.Message(), bag.Attrs()...)

	out := buf.String()
	assert.Contains(t, out, "INF list-orders-success")
	assert.Contains(t, out, "status=200")
	assert.Contains(t, out, "actor={requestorId=usr_123}")
	assert.Contains(t, out, "db={queryOrders={rows=5}}")
}

func TestConsoleHandler_ColorOutputAndErrors(t *testing.T) {
	var buf bytes.Buffer
	h := NewConsoleHandler(&buf, &ConsoleHandlerOptions{
		Level:   LevelDebug,
		NoColor: false,
	})
	logger := slog.New(h)

	ctx := NewContext(context.Background())
	SetResult(ctx, "charge-card", errors.New("card declined"))

	bag := BagFromContext(ctx)
	logger.LogAttrs(ctx, LevelError, bag.Message(), bag.Attrs()...)

	out := buf.String()
	require.Contains(t, out, ansiRed+"ERR"+ansiReset)
	assert.Contains(t, out, ansiBold+"charge-card-failure"+ansiReset)
	assert.Contains(t, out, `"card declined"`)
}

func TestConsoleHandler_WithAttrsAndWithGroup(t *testing.T) {
	var buf bytes.Buffer
	h := NewConsoleHandler(&buf, &ConsoleHandlerOptions{NoColor: true})
	logger := slog.New(h).With(slog.String("service", "billing")).WithGroup("req")

	logger.Info("processed", slog.String("id", "req_1"))

	out := buf.String()
	assert.Contains(t, out, "INF processed")
	assert.Contains(t, out, "service=billing")
	assert.Contains(t, out, "req={id=req_1}")
}

func TestConsoleHandler_LevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	h := NewConsoleHandler(&buf, &ConsoleHandlerOptions{
		Level:   LevelWarn,
		NoColor: true,
	})
	logger := slog.New(h)

	logger.Info("ignored info")
	assert.Empty(t, buf.String())

	logger.Warn("emitted warning")
	assert.Contains(t, buf.String(), "WRN emitted warning")
}
