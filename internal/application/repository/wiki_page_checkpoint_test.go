package repository

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestWikiPageCheckpointConcurrentSlugsPostgres(t *testing.T) {
	db, _ := finalizeAttemptPostgresDB(t)
	require.NoError(t, db.AutoMigrate(&types.WikiPage{}, &types.WikiPageRevision{}, &types.TaskPendingOp{}))
	var ids []int64
	for _, kid := range []string{"k1", "k2"} {
		row := &types.TaskPendingOp{
			TenantID: 1, TaskType: "wiki:ingest", Scope: "knowledge_base",
			ScopeID: "kb", DedupKey: kid,
			Payload: []byte(`{"future_field":42,"reduce_checkpoint":{"Updates":[` +
				`{"Slug":"entity/alpha","Type":"retract"},` +
				`{"Slug":"entity/alpha","Type":"entity"},` +
				`{"Slug":"entity/beta","Type":"entity"}],"Applied":[]}}`),
		}
		require.NoError(t, db.Create(row).Error)
		ids = append(ids, row.ID)
	}
	pages := []*types.WikiPage{
		{ID: "alpha", TenantID: 1, KnowledgeBaseID: "kb", Slug: "entity/alpha", Version: 1},
		{ID: "beta", TenantID: 1, KnowledgeBaseID: "kb", Slug: "entity/beta", Version: 1},
	}
	for _, page := range pages {
		require.NoError(t, db.Create(page).Error)
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, len(pages))
	for i, page := range pages {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			orderedIDs := []int64{ids[i], ids[1-i]}
			ctx := types.WithWikiPageCheckpoint(context.Background(), types.WikiPageCheckpoint{
				TenantID: 1, KnowledgeBaseID: "kb", Slug: page.Slug, PendingOpIDs: orderedIDs,
			})
			page.Content = "applied once"
			errs <- NewWikiPageRepository(db).UpdateWithRevision(ctx, page, nil)
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	for _, id := range ids {
		var row types.TaskPendingOp
		require.NoError(t, db.First(&row, id).Error)
		var op struct {
			Future     int `json:"future_field"`
			Checkpoint struct {
				Updates []json.RawMessage
				Applied []string
			} `json:"reduce_checkpoint"`
		}
		require.NoError(t, json.Unmarshal(row.Payload, &op))
		require.Equal(t, 42, op.Future)
		require.Empty(t, op.Checkpoint.Updates)
		require.ElementsMatch(t, []string{"entity/alpha", "entity/beta"}, op.Checkpoint.Applied)
	}
}
