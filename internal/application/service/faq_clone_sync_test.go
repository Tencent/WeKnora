package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestFAQCloneStatusSyncPreservesContentAndIndexState(t *testing.T) {
	for _, change := range []string{"enabled", "tag", "recommended", "answer_strategy"} {
		t.Run(change, func(t *testing.T) {
			f := newFAQWriteFixture(t)
			const body = "Restart the worker to reload the configuration."
			require.NoError(t, f.db.Model(&types.Chunk{}).Where("id IN ?", []string{"one", "two"}).
				Updates(map[string]any{
					"content": body, "status": int(types.ChunkStatusIndexed),
					"tag_id": "", "flags": 0, "metadata": types.JSON(`{"answer_strategy":"all"}`),
				}).Error)
			fields := map[string]any{}
			switch change {
			case "enabled":
				fields["is_enabled"] = false
			case "tag":
				fields["tag_id"] = "foreign"
			case "recommended":
				fields["flags"] = int(types.ChunkFlagRecommended)
			case "answer_strategy":
				fields["metadata"] = types.JSON(`{"answer_strategy":"random"}`)
			}
			require.NoError(t, f.db.Model(&types.Chunk{}).Where("id = ?", "two").Updates(fields).Error)
			pairs := []types.FAQChunkSyncPair{{SrcChunkID: "two", DstChunkID: "one"}}
			resolve := func(string) string { return "tag" }
			plan, err := f.svc.buildFAQStatusSyncPlan(f.ctx, 7, 7, pairs, resolve)
			require.NoError(t, err)
			require.Len(t, plan.Pairs, 1)
			err = f.svc.syncFAQChunkStatusBatch(f.ctx, f.kb, plan.Pairs, plan.SrcByID, plan.DstByID, resolve)
			require.NoError(t, err)
			chunk, err := f.chunks.GetChunkByID(f.ctx, 7, "one")
			require.NoError(t, err)
			require.Equal(t, body, chunk.Content)
			require.Equal(t, int(types.ChunkStatusIndexed), chunk.Status)
			plan, err = f.svc.buildFAQStatusSyncPlan(f.ctx, 7, 7, pairs, resolve)
			require.NoError(t, err)
			require.Empty(t, plan.Pairs, "the requested status changes must still be applied")
		})
	}
}
