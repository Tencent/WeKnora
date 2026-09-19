package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

// Wiring-level regression tests for incrementalIndexFAQEntry.
//
// The legacy (pre-upgrade MD5) source-ID coverage lives at the CALL SITES
// inside incrementalIndexFAQEntry — the removed-question path and the
// added/answer-changed path — not in the faqSimilarQuestionSourceIDs helper
// alone. These tests drive the real function through a recording retrieve
// engine so reverting either call site turns them red.

const faqIncrementalStoreID = "0193b8a0-2222-7000-8000-000000000002"

// faqIncrementalEngine records the source-ID level operations the FAQ
// incremental index performs, in call order.
type faqIncrementalEngine struct {
	interfaces.RetrieveEngineService
	ops              []string
	deletedSourceIDs []string
	indexedSourceIDs []string
}

func (e *faqIncrementalEngine) Support() []types.RetrieverType { return nil }

func (e *faqIncrementalEngine) EngineType() types.RetrieverEngineType { return "" }

func (e *faqIncrementalEngine) DeleteBySourceIDList(
	_ context.Context, sourceIDList []string, _ int, _ string,
) error {
	e.ops = append(e.ops, "delete")
	e.deletedSourceIDs = append(e.deletedSourceIDs, sourceIDList...)
	return nil
}

func (e *faqIncrementalEngine) BatchIndex(
	_ context.Context, _ embedding.Embedder, list []*types.IndexInfo, _ []types.RetrieverType,
) error {
	e.ops = append(e.ops, "index")
	for _, info := range list {
		e.indexedSourceIDs = append(e.indexedSourceIDs, info.SourceID)
	}
	return nil
}

type faqIncrementalRegistry struct {
	engine *faqIncrementalEngine
}

func (r *faqIncrementalRegistry) Register(interfaces.RetrieveEngineService) error { return nil }

func (r *faqIncrementalRegistry) GetRetrieveEngineService(
	types.RetrieverEngineType,
) (interfaces.RetrieveEngineService, error) {
	return r.engine, nil
}

func (r *faqIncrementalRegistry) GetAllRetrieveEngineServices() []interfaces.RetrieveEngineService {
	return []interfaces.RetrieveEngineService{r.engine}
}

func (r *faqIncrementalRegistry) GetByStoreID(string) (interfaces.RetrieveEngineService, error) {
	return r.engine, nil
}

func (r *faqIncrementalRegistry) GetOrLoadByStoreID(
	_ context.Context, _ uint64, _ string,
) (interfaces.RetrieveEngineService, error) {
	return r.engine, nil
}

type faqIncrementalKnowledgeRepo struct {
	interfaces.KnowledgeRepository
	updates int
}

func (r *faqIncrementalKnowledgeRepo) UpdateKnowledge(context.Context, *types.Knowledge) error {
	r.updates++
	return nil
}

func newFAQIncrementalFixture(t *testing.T) (
	*knowledgeService, *faqIncrementalEngine, *types.KnowledgeBase, *types.Chunk, *types.Knowledge,
) {
	t.Helper()

	engine := &faqIncrementalEngine{}
	storeID := faqIncrementalStoreID
	kb := &types.KnowledgeBase{
		ID:               "kb-1",
		TenantID:         1,
		VectorStoreID:    &storeID,
		EmbeddingModelID: "embed-1",
		FAQConfig:        &types.FAQConfig{IndexMode: types.FAQIndexModeQuestionAnswer},
	}
	chunk := &types.Chunk{
		ID:              "chunk-1",
		KnowledgeID:     "k-1",
		KnowledgeBaseID: "kb-1",
		Content:         "标准问",
	}
	knowledge := &types.Knowledge{ID: "k-1", TenantID: 1, KnowledgeBaseID: "kb-1"}
	svc := &knowledgeService{
		retrieveEngine: &faqIncrementalRegistry{engine: engine},
		ownership:      &fakeOwnership{owned: map[string]uint64{faqIncrementalStoreID: 1}},
		repo:           &faqIncrementalKnowledgeRepo{},
	}
	return svc, engine, kb, chunk, knowledge
}

func faqIncrementalCtx() context.Context {
	return context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
}

// Removing a similar question must delete BOTH the current-hash and the
// legacy (pre-upgrade MD5) source IDs, otherwise the old vector survives and
// keeps answering retrieval.
func TestIncrementalIndexFAQEntryRemovedQuestionDeletesLegacyHash(t *testing.T) {
	svc, engine, kb, chunk, knowledge := newFAQIncrementalFixture(t)

	require.NoError(t, svc.incrementalIndexFAQEntry(
		faqIncrementalCtx(), kb, knowledge, chunk, dimensionTestEmbedder{dimensions: 768},
		"标准问",
		[]string{"要删除的相似问", "保留的相似问"},
		nil,
		&types.FAQChunkMetadata{
			StandardQuestion: "标准问",
			SimilarQuestions: []string{"保留的相似问"},
		},
	))

	current := "chunk-1-" + hashQuestion("要删除的相似问")
	legacy := "chunk-1-" + hashQuestionLegacy("要删除的相似问")
	require.Equal(t, []string{current, legacy}, engine.deletedSourceIDs,
		"removed similar question must delete current and legacy source IDs")
	require.Empty(t, engine.indexedSourceIDs, "nothing changed, nothing to reindex")
	require.Equal(t, []string{"delete"}, engine.ops, "deletion must precede indexing")
}

// An answer change re-embeds under the current-hash ID; the legacy-hash vector
// of the same question must be scheduled for deletion too, and the deletion
// must run before the reindex so a hash collision cannot lose the new vector.
func TestIncrementalIndexFAQEntryAnswerChangeDeletesLegacyHash(t *testing.T) {
	svc, engine, kb, chunk, knowledge := newFAQIncrementalFixture(t)

	require.NoError(t, svc.incrementalIndexFAQEntry(
		faqIncrementalCtx(), kb, knowledge, chunk, dimensionTestEmbedder{dimensions: 768},
		"标准问",
		[]string{"保留的相似问"},
		[]string{"旧答案"},
		&types.FAQChunkMetadata{
			StandardQuestion: "标准问",
			SimilarQuestions: []string{"保留的相似问"},
			Answers:          []string{"新答案"},
		},
	))

	legacy := "chunk-1-" + hashQuestionLegacy("保留的相似问")
	current := "chunk-1-" + hashQuestion("保留的相似问")
	require.Contains(t, engine.deletedSourceIDs, legacy,
		"answer change must also delete the legacy-hash vector")
	require.Contains(t, engine.indexedSourceIDs, current,
		"the updated question must be reindexed under the current hash")
	require.Equal(t, []string{"delete", "index"}, engine.ops,
		"deletion must run before indexing")
}

// A question that is (re-)added must also drop its legacy-hash vector: an
// orphan left by a pre-upgrade delete would otherwise be resurrected next to
// the fresh vector, with the old answer still answering retrieval.
func TestIncrementalIndexFAQEntryAddedQuestionDeletesLegacyHash(t *testing.T) {
	svc, engine, kb, chunk, knowledge := newFAQIncrementalFixture(t)

	require.NoError(t, svc.incrementalIndexFAQEntry(
		faqIncrementalCtx(), kb, knowledge, chunk, dimensionTestEmbedder{dimensions: 768},
		"标准问",
		nil,
		nil,
		&types.FAQChunkMetadata{
			StandardQuestion: "标准问",
			SimilarQuestions: []string{"新加回的相似问"},
		},
	))

	legacy := "chunk-1-" + hashQuestionLegacy("新加回的相似问")
	current := "chunk-1-" + hashQuestion("新加回的相似问")
	require.Contains(t, engine.deletedSourceIDs, legacy,
		"a (re-)added question must drop its pre-upgrade legacy vector")
	require.Contains(t, engine.indexedSourceIDs, current)
	require.Equal(t, []string{"delete", "index"}, engine.ops)
}
