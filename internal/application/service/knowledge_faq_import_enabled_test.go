package service

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type faqTimeoutEngine struct {
	parentChildRetrieveEngine
	ctx     context.Context
	succeed bool
}

func (e *faqTimeoutEngine) BatchIndex(ctx context.Context, _ embedding.Embedder, _ []*types.IndexInfo, _ []types.RetrieverType) error {
	e.ctx = ctx
	if e.succeed {
		return nil
	}
	<-ctx.Done()
	return ctx.Err()
}

func (*faqTimeoutEngine) DeleteBySourceIDList(context.Context, []string, int, string) error {
	return nil
}

func TestFAQIndexOperationBudget(t *testing.T) {
	for _, incremental := range []bool{false, true} {
		name := "replace-and-merge-batch"
		if incremental {
			name = "incremental"
		}
		t.Run(name, func(t *testing.T) {
			for _, tc := range []struct {
				name    string
				parent  time.Duration
				want    time.Duration
				succeed bool
			}{
				{name: "no parent deadline", want: api.DefaultJSONTimeout},
				{name: "long task deadline", parent: 2 * time.Hour, want: api.DefaultJSONTimeout},
				{name: "shorter parent deadline", parent: api.DefaultJSONTimeout / 2, want: api.DefaultJSONTimeout / 2},
				{name: "success releases child", parent: 2 * time.Hour, succeed: true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					synctest.Test(t, func(t *testing.T) {
						engine := &faqTimeoutEngine{succeed: tc.succeed}
						store := "faq-timeout-store"
						svc := &knowledgeService{
							retrieveEngine: &fakeFanoutRegistry{byStore: map[string]interfaces.RetrieveEngineService{store: engine}},
							ownership:      &fakeOwnership{owned: map[string]uint64{store: 7}},
							repo:           &parentChildKnowledgeRepo{},
						}
						ctx := types.WithExecutionTenant(context.Background(), 7)
						ctx = context.WithValue(ctx, types.TenantInfoContextKey, &types.Tenant{ID: 7})
						if tc.parent > 0 {
							var cancel context.CancelFunc
							ctx, cancel = context.WithTimeout(ctx, tc.parent)
							defer cancel()
						}
						kb := &types.KnowledgeBase{ID: "kb", TenantID: 7, VectorStoreID: &store}
						knowledge := &types.Knowledge{ID: "faq"}
						chunk := &types.Chunk{ID: "one", KnowledgeID: "faq", KnowledgeBaseID: "kb", Content: "new question"}
						start := time.Now()
						var err error
						if incremental {
							err = svc.incrementalIndexFAQEntry(ctx, kb, knowledge, chunk, parentChildEmbedder{},
								"old question", nil, nil, &types.FAQChunkMetadata{StandardQuestion: "new question"})
						} else {
							err = svc.indexFAQChunks(ctx, kb, knowledge, []*types.Chunk{chunk}, parentChildEmbedder{}, false, false)
						}
						if tc.succeed {
							require.NoError(t, err)
						} else {
							require.ErrorIs(t, err, context.DeadlineExceeded)
						}
						require.Equal(t, tc.want, time.Since(start))
						require.NotNil(t, engine.ctx)
						require.Error(t, engine.ctx.Err(), "operation child must be canceled on return")
						if tc.parent == 0 || tc.parent > tc.want {
							require.NoError(t, ctx.Err(), "task context must remain usable for failure progress and cleanup")
						}
					})
				})
			}
		})
	}
}

func TestFAQImportPreservesEnabledStateInStorageAndIndex(t *testing.T) {
	for _, mode := range []string{types.FAQBatchModeAppend, types.FAQBatchModeReplace} {
		t.Run(mode, func(t *testing.T) {
			f := newFAQWriteFixture(t)
			sqlDB, err := f.db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
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
			// Index deduplication uses a map, so match by identity rather than slice order.
			indexedByChunkID := make(map[string]*types.IndexInfo, len(engine.indexed))
			for _, info := range engine.indexed {
				require.NotNil(t, info)
				indexedByChunkID[info.ChunkID] = info
			}
			require.Len(t, indexedByChunkID, len(saved))
			for i, wantEnabled := range []bool{true, false, true} {
				meta, err := saved[i].FAQMetadata()
				require.NoError(t, err)
				require.Equal(t, entries[i].StandardQuestion, meta.StandardQuestion)
				require.Equal(t, wantEnabled, saved[i].IsEnabled)
				info, ok := indexedByChunkID[saved[i].ID]
				require.True(t, ok, "chunk %s must be indexed", saved[i].ID)
				require.Equal(t, wantEnabled, info.IsEnabled)
				require.Equal(t, int(types.ChunkStatusIndexed), saved[i].Status)
				require.Equal(t, saved[i].SeqID, progress.SuccessEntries[i].SeqID)
			}
		})
	}
}
