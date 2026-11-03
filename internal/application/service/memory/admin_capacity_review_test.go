package memory

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// TestMemoryAdminLoweredPendingLimitOnServiceReplay 验证服务层去重不会绕过降低后的待确认上限。
func TestMemoryAdminLoweredPendingLimitOnServiceReplay(t *testing.T) {
	svc, db, tenants := newMemoryHarness(t)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	ctx := enabledCtx(t, tenants, 1, "alice")
	tenants.set(1, &types.MemoryConfig{Enabled: true, WriteMode: types.MemoryWriteAuto, MaxItems: 3})
	proposals := []types.MemoryItem{
		{Kind: types.MemoryKindFact, Topic: "editor", Content: "Alice prefers Vim"},
		{Kind: types.MemoryKindFact, Topic: "language", Content: "Bob writes Rust"},
		{Kind: types.MemoryKindFact, Topic: "runtime", Content: "Carol deploys Bun"},
	}
	var firstID string
	for i := range proposals {
		proposals[i].Origin, proposals[i].Inferred = types.MemoryOriginExtracted, true
		stored, err := svc.Remember(ctx, proposals[i])
		require.NoError(t, err)
		if i == 0 {
			firstID = stored.ID
		}
	}
	_, before, err := svc.ListItems(ctx, types.MemoryStatusPending, 50, 0)
	require.NoError(t, err)
	require.EqualValues(t, 3, before)
	tenants.set(1, &types.MemoryConfig{Enabled: true, WriteMode: types.MemoryWriteAuto, MaxItems: 1})
	replay := proposals[0]
	replay.Topic = "preferred text editor"
	replayed, err := svc.Remember(ctx, replay)
	require.NoError(t, err)
	require.Equal(t, firstID, replayed.ID, "a replay must not create another proposal")
	_, after, err := svc.ListItems(ctx, types.MemoryStatusPending, 50, 0)
	require.NoError(t, err)
	require.EqualValues(t, 1, after, "service-level duplicate detection must apply the new pending cap")
	scope, err := ResolveScope(ctx)
	require.NoError(t, err)
	committed, err := svc.repo.GetItem(ctx, scope, replayed.ID)
	require.NoError(t, err)
	require.Equal(t, types.MemoryStatusArchived, committed.Status, "the oldest replayed proposal is archived")
	require.Equal(t, committed.Status, replayed.Status, "the response must reflect committed status")
}
