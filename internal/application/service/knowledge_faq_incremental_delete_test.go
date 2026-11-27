package service

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

// faqDeleteRecordingEngine is an in-memory vector store used to replay issue
// #5958: it records every delete/index call and keeps rows keyed by SourceID,
// mirroring the Postgres repository semantics (DeleteByChunkIDList matches
// ChunkID, DeleteBySourceIDList matches SourceID).
type faqDeleteRecordingEngine struct {
	interfaces.RetrieveEngineService
	rows            map[string]*types.IndexInfo
	deletedBySource [][]string
	deletedByChunk  [][]string
	indexed         [][]*types.IndexInfo
}

func newFAQDeleteRecordingEngine() *faqDeleteRecordingEngine {
	return &faqDeleteRecordingEngine{rows: make(map[string]*types.IndexInfo)}
}

func (*faqDeleteRecordingEngine) EngineType() types.RetrieverEngineType {
	return types.PostgresRetrieverEngineType
}

func (*faqDeleteRecordingEngine) Support() []types.RetrieverType {
	return []types.RetrieverType{types.VectorRetrieverType}
}

func (*faqDeleteRecordingEngine) EstimateStorageSize(
	context.Context, embedding.Embedder, []*types.IndexInfo, []types.RetrieverType,
) int64 {
	return 0
}

func (e *faqDeleteRecordingEngine) BatchIndex(
	_ context.Context, _ embedding.Embedder, infos []*types.IndexInfo, _ []types.RetrieverType,
) error {
	e.indexed = append(e.indexed, infos)
	for _, info := range infos {
		cp := *info
		e.rows[info.SourceID] = &cp
	}
	return nil
}

func (e *faqDeleteRecordingEngine) DeleteBySourceIDList(
	_ context.Context, sourceIDs []string, _ int, _ string,
) error {
	e.deletedBySource = append(e.deletedBySource, sourceIDs)
	for _, id := range sourceIDs {
		delete(e.rows, id)
	}
	return nil
}

func (e *faqDeleteRecordingEngine) DeleteByChunkIDList(
	_ context.Context, chunkIDs []string, _ int, _ string,
) error {
	e.deletedByChunk = append(e.deletedByChunk, chunkIDs)
	for _, chunkID := range chunkIDs {
		for id, info := range e.rows {
			if info.ChunkID == chunkID {
				delete(e.rows, id)
			}
		}
	}
	return nil
}

func (e *faqDeleteRecordingEngine) DeleteByKnowledgeIDList(
	context.Context, []string, int, string,
) error {
	return nil
}

// seedLegacySeparateRows seeds the store exactly as a pre-fix full build did:
// the standard question under chunk.ID and every similar question under a
// positional source ID (<chunk-id>-<index>).
func (e *faqDeleteRecordingEngine) seedLegacySeparateRows(
	chunkID, standard string, similar []string, answers []string,
) {
	e.rows[chunkID] = &types.IndexInfo{
		Content:  standard,
		SourceID: chunkID,
		ChunkID:  chunkID,
	}
	for i, q := range similar {
		content := q
		if len(answers) > 0 {
			content = q + "\n" + strings.Join(answers, "\n")
		}
		id := fmt.Sprintf("%s-%d", chunkID, i)
		e.rows[id] = &types.IndexInfo{Content: content, SourceID: id, ChunkID: chunkID}
	}
}

func (e *faqDeleteRecordingEngine) containsContent(substr string) bool {
	for _, info := range e.rows {
		if strings.Contains(info.Content, substr) {
			return true
		}
	}
	return false
}

func (e *faqDeleteRecordingEngine) deletedByChunkCalled() bool {
	return len(e.deletedByChunk) > 0
}

// TestBuildFAQIndexInfoListSeparateModeUsesHashedSimilarQuestionIDs pins the
// root cause of #5958: the full build must address similar-question rows with
// the same content-hash source IDs the incremental path deletes, instead of
// positional <chunk-id>-<index> IDs.
func TestBuildFAQIndexInfoListSeparateModeUsesHashedSimilarQuestionIDs(t *testing.T) {
	kb := &types.KnowledgeBase{
		FAQConfig: &types.FAQConfig{
			IndexMode:         types.FAQIndexModeQuestionOnly,
			QuestionIndexMode: types.FAQQuestionIndexModeSeparate,
		},
	}
	chunk := &types.Chunk{ID: "chunk-5958", KnowledgeID: "faq", KnowledgeBaseID: "kb"}
	meta := &types.FAQChunkMetadata{
		StandardQuestion: "What is the postal address for support?",
		SimilarQuestions: []string{
			"Copper orchid voucher ZXQ-7921",
			"Where is support located?",
		},
		Answers: []string{"support@example.com"},
	}
	require.NoError(t, chunk.SetFAQMetadata(meta))

	// faqSimilarQuestionSourceID relies on NormalizeQuestion being
	// idempotent: the incremental path hashes already-normalized questions.
	for _, q := range meta.SimilarQuestions {
		require.Equal(t, types.NormalizeQuestion(q), types.NormalizeQuestion(types.NormalizeQuestion(q)))
	}

	infos, err := (&knowledgeService{}).buildFAQIndexInfoList(context.Background(), kb, chunk)
	require.NoError(t, err)
	require.Len(t, infos, 3)

	sourceIDs := make(map[string]string, len(infos))
	for _, info := range infos {
		sourceIDs[info.SourceID] = info.Content
	}
	require.Contains(t, sourceIDs, "chunk-5958")
	for _, q := range meta.SimilarQuestions {
		want := fmt.Sprintf("chunk-5958-%s", hashQuestion(types.NormalizeQuestion(q)))
		require.Contains(t, sourceIDs, want, "similar question %q must be indexed under its hashed source ID", q)
	}
	require.NotContains(t, sourceIDs, "chunk-5958-0")
	require.NotContains(t, sourceIDs, "chunk-5958-1")
}

// TestIncrementalIndexFAQEntryRemovesDeletedSimilarQuestion replays issue
// #5958 end to end at the service level (fake engine, sqlite fixture):
// a similar question removed by PUT must stop being retrievable, while the
// standard question and retained aliases stay indexed.
func TestIncrementalIndexFAQEntryRemovesDeletedSimilarQuestion(t *testing.T) {
	const (
		stdQ    = "What is the postal address for support?"
		deleted = "Copper orchid voucher ZXQ-7921"
		kept    = "Where is support located?"
		answer  = "support@example.com"
	)

	setup := func(t *testing.T) (*faqWriteFixture, *faqDeleteRecordingEngine, *types.Knowledge, *types.Chunk) {
		t.Helper()
		f := newFAQWriteFixture(t)
		f.kb.FAQConfig = &types.FAQConfig{
			IndexMode:         types.FAQIndexModeQuestionOnly,
			QuestionIndexMode: types.FAQQuestionIndexModeSeparate,
		}
		engine := newFAQDeleteRecordingEngine()
		f.svc.retrieveEngine = &fakeFanoutRegistry{
			byStore: map[string]interfaces.RetrieveEngineService{*f.kb.VectorStoreID: engine},
		}
		f.svc.modelService = parentChildModelService{embedder: parentChildEmbedder{}}
		ctx := context.WithValue(f.ctx, types.TenantIDContextKey, uint64(7))
		knowledge, err := f.svc.repo.GetKnowledgeByID(ctx, 7, "faq")
		require.NoError(t, err)
		chunk := &types.Chunk{
			ID: "one", KnowledgeID: "faq", KnowledgeBaseID: "kb",
			TagID: "tag", IsEnabled: true,
		}
		return f, engine, knowledge, chunk
	}
	call := func(
		t *testing.T, f *faqWriteFixture, knowledge *types.Knowledge, chunk *types.Chunk,
		oldStd string, oldSimilar, oldAnswers []string, newMeta *types.FAQChunkMetadata,
	) {
		t.Helper()
		ctx := context.WithValue(f.ctx, types.TenantIDContextKey, uint64(7))
		require.NoError(t, chunk.SetFAQMetadata(newMeta))
		require.NoError(t, f.svc.incrementalIndexFAQEntry(
			ctx, f.kb, knowledge, chunk, parentChildEmbedder{},
			oldStd, oldSimilar, oldAnswers, newMeta,
		))
	}

	t.Run("issue scenario: deleted similar question stops matching", func(t *testing.T) {
		f, engine, knowledge, chunk := setup(t)
		engine.seedLegacySeparateRows("one", stdQ, []string{deleted}, nil)
		call(t, f, knowledge, chunk, stdQ, []string{deleted}, []string{answer},
			&types.FAQChunkMetadata{StandardQuestion: stdQ, SimilarQuestions: nil, Answers: []string{answer}})
		require.False(t, engine.containsContent(deleted), "deleted similar question must not remain indexed")
		require.True(t, engine.containsContent(stdQ), "standard question must stay indexed")
	})

	t.Run("retained alias survives when another alias is deleted", func(t *testing.T) {
		f, engine, knowledge, chunk := setup(t)
		engine.seedLegacySeparateRows("one", stdQ, []string{kept, deleted}, nil)
		call(t, f, knowledge, chunk, stdQ, []string{kept, deleted}, []string{answer},
			&types.FAQChunkMetadata{
				StandardQuestion: stdQ, SimilarQuestions: []string{kept}, Answers: []string{answer},
			})
		require.False(t, engine.containsContent(deleted), "deleted similar question must not remain indexed")
		require.True(t, engine.containsContent(kept), "retained alias must stay indexed")
		require.True(t, engine.containsContent(stdQ), "standard question must stay indexed")
	})

	t.Run("pure add stays incremental and keeps legacy rows", func(t *testing.T) {
		f, engine, knowledge, chunk := setup(t)
		engine.seedLegacySeparateRows("one", stdQ, []string{kept}, nil)
		call(t, f, knowledge, chunk, stdQ, []string{kept}, []string{answer},
			&types.FAQChunkMetadata{
				StandardQuestion: stdQ, SimilarQuestions: []string{kept, deleted}, Answers: []string{answer},
			})
		require.False(t, engine.deletedByChunkCalled(), "add-only edit must not trigger a chunk-wide rebuild")
		require.True(t, engine.containsContent(kept), "existing alias must stay indexed")
		// The incremental path indexes normalized content, so the added alias
		// is stored lower-cased under its hashed source ID.
		addedID := fmt.Sprintf("one-%s", hashQuestion(types.NormalizeQuestion(deleted)))
		added, ok := engine.rows[addedID]
		require.True(t, ok, "added alias must be indexed under its hashed source ID")
		require.Contains(t, added.Content, types.NormalizeQuestion(deleted))
	})

	t.Run("answer change drops stale similar rows", func(t *testing.T) {
		f, engine, knowledge, chunk := setup(t)
		f.kb.FAQConfig.IndexMode = types.FAQIndexModeQuestionAnswer
		engine.seedLegacySeparateRows("one", stdQ, []string{kept}, []string{"old answer"})
		call(t, f, knowledge, chunk, stdQ, []string{kept}, []string{"old answer"},
			&types.FAQChunkMetadata{
				StandardQuestion: stdQ, SimilarQuestions: []string{kept}, Answers: []string{"new answer"},
			})
		require.False(t, engine.containsContent("old answer"), "stale answer content must not remain indexed")
		require.True(t, engine.containsContent("new answer"), "new answer content must be indexed")
		require.True(t, engine.containsContent(kept), "alias must stay indexed")
	})
}
