package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

func TestFAQImportPreservesEnabledStateInStorageAndIndex(t *testing.T) {
	for _, mode := range []string{types.FAQBatchModeAppend, types.FAQBatchModeReplace} {
		t.Run(mode, func(t *testing.T) {
			f := newFAQWriteFixture(t)
			// Keep the real FAQ container and tag, but start with no FAQ entries.
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
			enabled, disabled := true, false
			entries := []types.FAQEntryPayload{
				{
					StandardQuestion: "enabled FAQ", Answers: []string{"enabled answer"},
					TagID: 11, IsEnabled: &enabled,
				},
				{
					StandardQuestion: "disabled FAQ", Answers: []string{"disabled answer"},
					TagID: 11, IsEnabled: &disabled,
				},
				{StandardQuestion: "default FAQ", Answers: []string{"default answer"}, TagID: 11},
			}
			progress := &types.FAQImportProgress{TaskID: "enabled-import", KBID: f.kb.ID}
			require.NoError(t, f.svc.executeFAQImport(f.ctx, progress.TaskID, f.kb.ID,
				&types.FAQBatchUpsertPayload{Mode: mode, Entries: entries}, f.kb.TenantID, 0, progress))
			var saved []types.Chunk
			require.NoError(t, f.db.Where("knowledge_id = ?", "faq").Order("seq_id").Find(&saved).Error)
			require.Len(t, saved, 3)
			require.Len(t, engine.indexed, 3)
			require.Len(t, progress.SuccessEntries, 3)
			for i, wantEnabled := range []bool{true, false, true} {
				meta, err := saved[i].FAQMetadata()
				require.NoError(t, err)
				require.Equal(t, entries[i].StandardQuestion, meta.StandardQuestion)
				require.Equal(t, wantEnabled, saved[i].IsEnabled)
				require.Equal(t, wantEnabled, engine.indexed[i].IsEnabled)
				require.Equal(t, saved[i].ID, engine.indexed[i].ChunkID)
				require.Equal(t, int(types.ChunkStatusIndexed), saved[i].Status)
				require.Equal(t, saved[i].SeqID, progress.SuccessEntries[i].SeqID)
			}
		})
	}
}
