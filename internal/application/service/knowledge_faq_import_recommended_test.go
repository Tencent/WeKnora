package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

func TestFAQImportPreservesRecommendationInStorageAndIndex(t *testing.T) {
	for _, mode := range []string{types.FAQBatchModeAppend, types.FAQBatchModeReplace} {
		t.Run(mode, func(t *testing.T) {
			f := newFAQWriteFixture(t)
			sqlDB, err := f.db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
			require.NoError(t, f.db.Where("id = ?", "one").Delete(&types.Chunk{}).Error)
			engine := &parentChildRetrieveEngine{}
			f.svc.retrieveEngine = &fakeFanoutRegistry{
				byStore: map[string]interfaces.RetrieveEngineService{*f.kb.VectorStoreID: engine},
			}
			f.svc.modelService = parentChildModelService{embedder: parentChildEmbedder{}}
			f.svc.chunkService = &chunkService{
				chunkRepository: f.chunks, knowledgeRepo: f.svc.repo,
				kbRepository: &imageReaderKBRepo{kb: f.kb},
			}
			yes, no := true, false
			entries := []types.FAQEntryPayload{
				{StandardQuestion: "recommended FAQ", Answers: []string{"yes"}, TagID: 11, IsRecommended: &yes},
				{StandardQuestion: "not recommended FAQ", Answers: []string{"no"}, TagID: 11, IsRecommended: &no},
				{StandardQuestion: "default FAQ", Answers: []string{"default"}, TagID: 11},
			}
			progress := &types.FAQImportProgress{TaskID: "recommendation-import", KBID: f.kb.ID}
			require.NoError(t, f.svc.executeFAQImport(f.ctx, progress.TaskID, f.kb.ID,
				&types.FAQBatchUpsertPayload{Mode: mode, Entries: entries}, f.kb.TenantID, 0, progress))
			var saved []types.Chunk
			require.NoError(t, f.db.Where("knowledge_id = ?", "faq").Order("seq_id").Find(&saved).Error)
			require.Len(t, saved, len(entries))
			indexed := make(map[string]*types.IndexInfo)
			for _, info := range engine.indexed {
				indexed[info.ChunkID] = info
			}
			for i, wantRecommended := range []bool{true, false, true} {
				require.Equal(t, wantRecommended, saved[i].Flags.HasFlag(types.ChunkFlagRecommended))
				require.NotNil(t, indexed[saved[i].ID])
				require.Equal(t, wantRecommended, indexed[saved[i].ID].IsRecommended)
				require.Equal(t, uint64(7), saved[i].TenantID)
			}
			var foreign types.Chunk
			require.NoError(t, f.db.First(&foreign, "id = ?", "two").Error)
			require.Equal(t, "other", foreign.KnowledgeBaseID)
		})
	}
}
