// Package clog tests for FieldBag
package clog

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureEvent logs the FieldBag's attributes using slog.NewJSONHandler and returns the unmarshaled JSON.
func captureEvent(t *testing.T, bag *FieldBag) map[string]any {
	t.Helper()
	var buf bytes.Buffer
	h := NewJSONHandler(&buf, nil)
	logger := slog.New(h)
	msg := "canonical-log-line"
	if bag != nil {
		msg = bag.Message()
	}
	logger.LogAttrs(context.Background(), slog.LevelInfo, msg, bag.Attrs()...)

	var out map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &out))
	return out
}

func TestFieldBag_RootFields(t *testing.T) {
	bag := &FieldBag{}
	bag.Set("key1", "val1")
	bag.Set("key2", 42)

	out := captureEvent(t, bag)
	assert.Equal(t, "val1", out["key1"])
	assert.Equal(t, float64(42), out["key2"])
}

func TestFieldBag_SetMany(t *testing.T) {
	bag := &FieldBag{}
	bag.SetMany(Fields{
		"k1": "v1",
		"k2": 100,
	})

	out := captureEvent(t, bag)
	assert.Equal(t, "v1", out["k1"])
	assert.Equal(t, float64(100), out["k2"])
}

func TestFieldBag_GroupFields(t *testing.T) {
	bag := &FieldBag{}
	bag.setGroup("user", "id", "usr-123")
	bag.setGroup("user", "is_admin", false)

	out := captureEvent(t, bag)
	userGroup, ok := out["user"].(map[string]any)
	require.True(t, ok, "expected 'user' to be a JSON object")
	assert.Equal(t, "usr-123", userGroup["id"])
	assert.Equal(t, false, userGroup["is_admin"])
}

func TestFieldBag_SetGroupMany(t *testing.T) {
	bag := &FieldBag{}
	bag.setGroupMany("event", map[string]any{
		"id":    "evt-456",
		"title": "Concert",
	})

	out := captureEvent(t, bag)
	eventGroup, ok := out["event"].(map[string]any)
	require.True(t, ok, "expected 'event' to be a JSON object")
	assert.Equal(t, "evt-456", eventGroup["id"])
	assert.Equal(t, "Concert", eventGroup["title"])
}

func TestFieldBag_SameKeyOverwrite(t *testing.T) {
	bag := &FieldBag{}
	bag.setGroup("user", "id", "first")
	bag.setGroup("user", "id", "second")

	out := captureEvent(t, bag)
	userGroup, ok := out["user"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "second", userGroup["id"])
}

func TestFieldBag_MultipleGroups(t *testing.T) {
	bag := &FieldBag{}
	bag.setGroup("user", "id", "usr-1")
	bag.setGroup("campaign", "id", "cmp-1")
	bag.setGroup("campaign", "status", "sent")

	out := captureEvent(t, bag)

	userGroup, ok := out["user"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "usr-1", userGroup["id"])

	campaignGroup, ok := out["campaign"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "cmp-1", campaignGroup["id"])
	assert.Equal(t, "sent", campaignGroup["status"])
}

func TestFieldBag_ConcurrentWrites(t *testing.T) {
	bag := &FieldBag{}
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			bag.Set("root_key", n)
			bag.setGroup("group", "key", n)
		}(i)
	}
	wg.Wait()

	out := captureEvent(t, bag)
	assert.Contains(t, out, "root_key")
	assert.Contains(t, out, "group")
}

func TestFieldBag_NilBag_Attrs(t *testing.T) {
	var bag *FieldBag
	var buf bytes.Buffer
	logger := slog.New(NewJSONHandler(&buf, nil))
	logger.LogAttrs(context.Background(), slog.LevelInfo, "ok", bag.Attrs()...)

	var out map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &out))
	assert.Equal(t, "ok", out["message"])
	assert.NotContains(t, out, "user")
}

func TestFieldBag_ContextHelpers(t *testing.T) {
	ctx := context.Background()

	// BagFromContext on empty context returns nil
	assert.Nil(t, BagFromContext(ctx))

	// WithField on empty context is a no-op
	ctx2 := WithField(ctx, "k", "v")
	assert.Equal(t, ctx, ctx2)

	// WithFields on empty context is a no-op
	ctxFields := WithFields(ctx, Fields{"k": "v"})
	assert.Equal(t, ctx, ctxFields)

	// WithMessage on empty context is a no-op
	ctxMsg := WithMessage(ctx, "msg")
	assert.Equal(t, ctx, ctxMsg)

	// NewContext creates a FieldBag and stores it
	ctx3 := NewContext(ctx)
	bag := BagFromContext(ctx3)
	require.NotNil(t, bag)

	ctx3 = WithField(ctx3, "root", "r1")
	ctx3 = WithFields(ctx3, Fields{"extra_root": "r2"})
	ctx3 = WithGroupField(ctx3, "user", "id", "u1")
	ctx3 = WithGroupFields(ctx3, "event", map[string]any{"id": "e1"})
	ctx3 = WithMessage(ctx3, "custom-message")

	bag = BagFromContext(ctx3)
	require.NotNil(t, bag)

	out := captureEvent(t, bag)
	assert.Equal(t, "r1", out["root"])
	assert.Equal(t, "r2", out["extra_root"])

	userGroup, ok := out["user"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "u1", userGroup["id"])

	eventGroup, ok := out["event"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "e1", eventGroup["id"])

	assert.Equal(t, "custom-message", bag.Message())
}

func TestFieldBag_MessageDefault(t *testing.T) {
	bag := &FieldBag{}
	assert.Equal(t, "canonical-log-line", bag.Message())
}

func TestFieldBag_MessageOverride(t *testing.T) {
	bag := &FieldBag{}
	bag.setMessage("my-override")
	assert.Equal(t, "my-override", bag.Message())
}

func TestFieldBag_GetGroup(t *testing.T) {
	bag := &FieldBag{}
	bag.setGroup("actor", "id", "u1")
	bag.setGroup("actor", "is_admin", true)

	got := bag.getGroup("actor")
	require.NotNil(t, got)
	assert.Equal(t, "u1", got["id"])
	assert.Equal(t, true, got["is_admin"])
}

func TestFieldBag_GetGroup_Missing(t *testing.T) {
	bag := &FieldBag{}
	assert.Nil(t, bag.getGroup("nonexistent"))
}

func TestFieldBag_GetGroup_NilBag(t *testing.T) {
	var bag *FieldBag
	assert.Nil(t, bag.getGroup("actor"))
}

func TestFieldBag_GetGroup_IsCopy(t *testing.T) {
	bag := &FieldBag{}
	bag.setGroup("actor", "id", "u1")

	got := bag.getGroup("actor")
	got["extra"] = "injected"

	got2 := bag.getGroup("actor")
	assert.NotContains(t, got2, "extra")
}

func TestFieldBag_Snapshot_Empty(t *testing.T) {
	bag := &FieldBag{}
	snap := bag.snapshot()
	assert.NotNil(t, snap)
	assert.Empty(t, snap)
}

func TestFieldBag_Snapshot_NilBag(t *testing.T) {
	var bag *FieldBag
	assert.Nil(t, bag.snapshot())
}

func TestFieldBag_Snapshot_RootAndGroups(t *testing.T) {
	bag := &FieldBag{}
	bag.Set("job", "cron")
	bag.setGroup("actor", "id", "u1")
	bag.setGroup("actor", "is_admin", false)
	bag.setGroup("campaign", "id", "c1")

	snap := bag.snapshot()
	require.NotNil(t, snap)

	assert.Equal(t, "cron", snap["job"])

	actor, ok := snap["actor"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "u1", actor["id"])
	assert.Equal(t, false, actor["is_admin"])

	campaign, ok := snap["campaign"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "c1", campaign["id"])
}

func TestFieldBag_Snapshot_IsCopy(t *testing.T) {
	bag := &FieldBag{}
	bag.Set("job", "cron")
	bag.setGroup("actor", "id", "u1")

	snap := bag.snapshot()
	snap["job"] = "mutated"
	actorSnap := snap["actor"].(map[string]any)
	actorSnap["id"] = "mutated"

	assert.Equal(t, "cron", bag.root["job"])
	got := bag.getGroup("actor")
	assert.Equal(t, "u1", got["id"])
}

// --- Nested sub-group tests ---

func TestFieldBag_NestedGroup_TwoLevels(t *testing.T) {
	bag := &FieldBag{}
	bag.setGroupMany("db.getCampaign", map[string]any{
		"table":     "campaigns",
		"operation": "findFirst",
	})

	out := captureEvent(t, bag)
	db, ok := out["db"].(map[string]any)
	require.True(t, ok, "expected 'db' to be a JSON object")
	getCampaign, ok := db["getCampaign"].(map[string]any)
	require.True(t, ok, "expected 'db.getCampaign' to be a JSON object")
	assert.Equal(t, "campaigns", getCampaign["table"])
	assert.Equal(t, "findFirst", getCampaign["operation"])
}

func TestFieldBag_NestedGroup_ThreeLevels(t *testing.T) {
	bag := &FieldBag{}
	bag.setGroup("db.getCampaign.timing", "durationMs", float64(42))

	out := captureEvent(t, bag)
	db := out["db"].(map[string]any)
	getCampaign := db["getCampaign"].(map[string]any)
	timing := getCampaign["timing"].(map[string]any)
	assert.Equal(t, float64(42), timing["durationMs"])
}

func TestFieldBag_NestedGroup_DepthTruncation(t *testing.T) {
	bag := &FieldBag{}
	// "a.b.c.d" should truncate to "a.b.c" — field goes into level 3
	bag.setGroup("a.b.c.d", "key", "value")

	out := captureEvent(t, bag)
	a := out["a"].(map[string]any)
	b := a["b"].(map[string]any)
	c := b["c"].(map[string]any)
	assert.Equal(t, "value", c["key"])
	assert.Nil(t, c["d"])
}

func TestFieldBag_NestedGroup_SiblingMerge(t *testing.T) {
	bag := &FieldBag{}
	bag.setGroupMany("db.getCampaign", map[string]any{"table": "campaigns"})
	bag.setGroupMany("db.getAttendees", map[string]any{"table": "attendees"})

	out := captureEvent(t, bag)
	db := out["db"].(map[string]any)

	getCampaign := db["getCampaign"].(map[string]any)
	assert.Equal(t, "campaigns", getCampaign["table"])

	getAttendees := db["getAttendees"].(map[string]any)
	assert.Equal(t, "attendees", getAttendees["table"])
}

func TestFieldBag_NestedGroup_FlatAndNestedCoexist(t *testing.T) {
	bag := &FieldBag{}
	bag.setGroup("db", "connectionPool", "active")
	bag.setGroupMany("db.getCampaign", map[string]any{"table": "campaigns"})

	out := captureEvent(t, bag)
	db := out["db"].(map[string]any)
	assert.Equal(t, "active", db["connectionPool"])

	getCampaign := db["getCampaign"].(map[string]any)
	assert.Equal(t, "campaigns", getCampaign["table"])
}

func TestFieldBag_HasGroup_PartialPath(t *testing.T) {
	bag := &FieldBag{}
	bag.setGroup("db.getCampaign", "table", "campaigns")

	assert.True(t, bag.hasGroup("db"))
	assert.True(t, bag.hasGroup("db.getCampaign"))
	assert.False(t, bag.hasGroup("db.nonexistent"))
	assert.False(t, bag.hasGroup("other"))
}

func TestFieldBag_GetGroup_PartialPath(t *testing.T) {
	bag := &FieldBag{}
	bag.setGroupMany("db.getCampaign", map[string]any{"table": "campaigns"})
	bag.setGroupMany("db.getAttendees", map[string]any{"table": "attendees"})

	dbGroup := bag.getGroup("db")
	require.NotNil(t, dbGroup)

	getCampaign, ok := dbGroup["getCampaign"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "campaigns", getCampaign["table"])

	getAttendees, ok := dbGroup["getAttendees"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "attendees", getAttendees["table"])
}

func TestFieldBag_GetGroup_FullPath(t *testing.T) {
	bag := &FieldBag{}
	bag.setGroupMany("db.getCampaign", map[string]any{"table": "campaigns", "operation": "findFirst"})

	got := bag.getGroup("db.getCampaign")
	require.NotNil(t, got)
	assert.Equal(t, "campaigns", got["table"])
	assert.Equal(t, "findFirst", got["operation"])
	assert.Nil(t, got["getCampaign"])
}

func TestFieldBag_GetGroup_DeepCopy(t *testing.T) {
	bag := &FieldBag{}
	bag.setGroupMany("db.getCampaign", map[string]any{"table": "campaigns"})

	got := bag.getGroup("db")
	getCampaign := got["getCampaign"].(map[string]any)
	getCampaign["table"] = "mutated"

	original := bag.getGroup("db.getCampaign")
	assert.Equal(t, "campaigns", original["table"])
}

func TestFieldBag_Snapshot_WithNestedGroups(t *testing.T) {
	bag := &FieldBag{}
	bag.Set("job", "cron")
	bag.setGroupMany("db.getCampaign", map[string]any{"table": "campaigns"})

	snap := bag.snapshot()
	assert.Equal(t, "cron", snap["job"])

	db, ok := snap["db"].(map[string]any)
	require.True(t, ok)
	getCampaign, ok := db["getCampaign"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "campaigns", getCampaign["table"])

	getCampaign["table"] = "mutated"
	original := bag.getGroup("db.getCampaign")
	assert.Equal(t, "campaigns", original["table"])
}

func TestFieldBag_NestedGroup_EdgeCase_EmptySegments(t *testing.T) {
	bag := &FieldBag{}
	bag.setGroup(".db.method", "k1", "v1")
	bag.setGroup("db.method.", "k2", "v2")
	bag.setGroup("db..method", "k3", "v3")

	got := bag.getGroup("db.method")
	require.NotNil(t, got)
	assert.Equal(t, "v1", got["k1"])
	assert.Equal(t, "v2", got["k2"])
	assert.Equal(t, "v3", got["k3"])
}

func TestFieldBag_NestedGroup_ConcurrentWrites(t *testing.T) {
	bag := &FieldBag{}
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			group := "db.method" + string(rune('A'+n%26))
			bag.setGroup(group, "n", n)
		}(i)
	}
	wg.Wait()

	out := captureEvent(t, bag)
	assert.Contains(t, out, "db")
}

func TestFieldBag_NestedGroup_BackwardCompat_FlatGroups(t *testing.T) {
	bag := &FieldBag{}
	bag.setGroup("user", "id", "usr-123")
	bag.setGroup("user", "isAdmin", false)
	bag.setGroupMany("campaign", map[string]any{"id": "cmp-1", "status": "sent"})

	out := captureEvent(t, bag)

	userGroup := out["user"].(map[string]any)
	assert.Equal(t, "usr-123", userGroup["id"])
	assert.Equal(t, false, userGroup["isAdmin"])

	campaignGroup := out["campaign"].(map[string]any)
	assert.Equal(t, "cmp-1", campaignGroup["id"])
	assert.Equal(t, "sent", campaignGroup["status"])
}

func TestFieldBag_HasWarning(t *testing.T) {
	var nilBag *FieldBag
	assert.False(t, nilBag.hasWarning())

	bag := &FieldBag{}
	assert.False(t, bag.hasWarning())

	bag.Set("warning", "disk space low")
	assert.True(t, bag.hasWarning())

	bag2 := &FieldBag{}
	bag2.Set("warnings", []string{"w1", "w2"})
	assert.True(t, bag2.hasWarning())

	bag3 := &FieldBag{}
	bag3.setGroup("db.query", "warning", "slow query")
	assert.True(t, bag3.hasWarning())

	bag4 := &FieldBag{}
	bag4.setGroup("warning", "detail", "something")
	assert.True(t, bag4.hasWarning())
}
