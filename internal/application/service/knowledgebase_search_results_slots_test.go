package service

import (
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestSearchResultsFillSlotsAfterChunkHydration(t *testing.T) {
	for _, state := range []string{"missing", "disabled", "processing", "failed", "non-searchable"} {
		for _, skipEnrichment := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/skipEnrichment=%t", state, skipEnrichment), func(t *testing.T) {
				svc, db := newKnowledgeSharedAccessService(t, nil)
				require.NoError(t, db.AutoMigrate(&types.Chunk{}))
				search := &knowledgeBaseService{kgRepo: svc.repo, chunkRepo: repository.NewChunkRepository(db)}
				seedKnowledge(t, db, &types.Knowledge{
					ID: "doc", TenantID: 1, KnowledgeBaseID: "kb", Type: "file",
					ParseStatus: types.ParseStatusCompleted,
				})
				stale := &types.Chunk{
					ID: "stale", TenantID: 1, KnowledgeBaseID: "kb", KnowledgeID: "doc",
					NextChunkID: "overflow", Content: "stale content", ChunkType: types.ChunkTypeText,
					IsEnabled: true, IndexStatus: "ready",
				}
				switch state {
				case "disabled":
					stale.IsEnabled = false
				case "processing", "failed":
					stale.IndexStatus = state
				case "non-searchable":
					stale.ChunkType = types.ChunkTypeParentText
				}
				if state != "missing" {
					require.NoError(t, db.Create(stale).Error)
					// GORM's default:true tag also applies to explicit false inserts.
					if state == "disabled" {
						require.NoError(t, db.Model(stale).Update("is_enabled", false).Error)
					}
				}
				var hits []*types.IndexWithScore
				for i, id := range []string{"stale", "valid", "overflow"} {
					if i > 0 {
						require.NoError(t, db.Create(&types.Chunk{
							ID: id, TenantID: 1, KnowledgeBaseID: "kb", KnowledgeID: "doc",
							Content: id, ChunkType: types.ChunkTypeText, IsEnabled: true, IndexStatus: "ready",
						}).Error)
					}
					hits = append(hits, &types.IndexWithScore{
						ChunkID: id, KnowledgeID: "doc", Score: 0.9 - float64(i)*0.1,
					})
				}
				empty, err := search.processSearchResults(newSharedAccessContext(), hits[:1], 1, skipEnrichment)
				require.NoError(t, err)
				require.Empty(t, empty)
				results, err := search.processSearchResults(newSharedAccessContext(), hits, 1, skipEnrichment)
				require.NoError(t, err)
				require.Len(t, results, 1, "an invalid higher-ranked index hit must not consume the only result slot")
				require.Equal(t, "valid", results[0].ID)
				require.InDelta(t, 0.8, results[0].Score, 0.0001)
			})
		}
	}
}

func TestSearchResultsEnrichOnlySelectedChunks(t *testing.T) {
	for _, owner := range []uint64{1, 2} {
		t.Run(fmt.Sprintf("tenant=%d", owner), func(t *testing.T) {
			shares := &fakeKBShareService{allowedKBs: map[string]bool{"kb": true}}
			svc, db := newKnowledgeSharedAccessService(t, shares)
			require.NoError(t, db.AutoMigrate(&types.Chunk{}))
			search := &knowledgeBaseService{
				kgRepo: svc.repo, chunkRepo: repository.NewChunkRepository(db), kbShareService: shares,
			}
			seedKnowledge(t, db, &types.Knowledge{
				ID: "doc", TenantID: owner, KnowledgeBaseID: "kb", Type: "file",
				ParseStatus: types.ParseStatusCompleted,
			})
			for _, chunk := range []*types.Chunk{
				{ID: "primary", NextChunkID: "neighbor"},
				{ID: "unselected", NextChunkID: "hidden"},
				{ID: "neighbor"},
				{ID: "hidden"},
			} {
				chunk.TenantID = owner
				chunk.KnowledgeID = "doc"
				chunk.KnowledgeBaseID = "kb"
				chunk.Content = chunk.ID
				chunk.ChunkType = types.ChunkTypeText
				chunk.IsEnabled = true
				require.NoError(t, db.Create(chunk).Error)
			}
			hits := []*types.IndexWithScore{
				{ChunkID: "primary", KnowledgeID: "doc", Score: 0.9},
				{ChunkID: "unselected", KnowledgeID: "doc", Score: 0.8},
			}
			for _, skipEnrichment := range []bool{false, true} {
				results, err := search.processSearchResults(newSharedAccessContext(), hits, 1, skipEnrichment)
				require.NoError(t, err)
				require.NotEmpty(t, results)
				require.Equal(t, "primary", results[0].ID)
				ids := make([]string, 0, len(results))
				for _, result := range results {
					ids = append(ids, result.ID)
				}
				if skipEnrichment {
					require.Equal(t, []string{"primary"}, ids)
				} else {
					require.Equal(t, []string{"primary", "neighbor"}, ids)
				}
			}
		})
	}
}
