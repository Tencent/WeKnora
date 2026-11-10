package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

// tenantScopedFAQChunkRepo mimics the SQL repository: ListChunksByID filters
// on the caller's tenant, ListChunksByIDOnly does not.
type tenantScopedFAQChunkRepo struct {
	interfaces.ChunkRepository
	chunks map[string]*types.Chunk
}

func (r *tenantScopedFAQChunkRepo) ListChunksByID(
	_ context.Context, tenantID uint64, ids []string,
) ([]*types.Chunk, error) {
	var out []*types.Chunk
	for _, id := range ids {
		if c := r.chunks[id]; c != nil && c.TenantID == tenantID {
			out = append(out, c)
		}
	}
	return out, nil
}

func (r *tenantScopedFAQChunkRepo) ListChunksByIDOnly(_ context.Context, ids []string) ([]*types.Chunk, error) {
	var out []*types.Chunk
	for _, id := range ids {
		if c := r.chunks[id]; c != nil {
			out = append(out, c)
		}
	}
	return out, nil
}

func faqChunkWithNegative(t *testing.T, id string, tenantID uint64, kbID, negative string) *types.Chunk {
	t.Helper()
	c := &types.Chunk{ID: id, TenantID: tenantID, KnowledgeBaseID: kbID, ChunkType: types.ChunkTypeFAQ}
	require.NoError(t, c.SetFAQMetadata(&types.FAQChunkMetadata{
		StandardQuestion:  "How do refunds work?",
		Answers:           []string{"Within 7 days."},
		NegativeQuestions: []string{negative},
	}))
	return c
}

// An FAQ KB searched together with a document KB (document KB primary), and
// an FAQ KB shared from another workspace, must both honour negative
// questions. Before, the filter ran only when the primary KB was an FAQ KB and
// looked chunks up in the caller's workspace only; a chunk it could not find
// was kept.
func TestApplyFAQPostProcessingFiltersNegativeQuestionsAcrossScope(t *testing.T) {
	repo := &tenantScopedFAQChunkRepo{chunks: map[string]*types.Chunk{
		"own-faq":    faqChunkWithNegative(t, "own-faq", 1, "kb-faq", "refund for gift cards"),
		"shared-faq": faqChunkWithNegative(t, "shared-faq", 2, "kb-shared-faq", "refund for gift cards"),
		"doc":        {ID: "doc", TenantID: 1, KnowledgeBaseID: "kb-doc", ChunkType: types.ChunkTypeText},
	}}
	s := &knowledgeBaseService{
		chunkRepo:      repo,
		kbShareService: &fakeKBShareService{allowedKBs: map[string]bool{"kb-shared-faq": true}},
	}
	docKB := &types.KnowledgeBase{ID: "kb-doc", TenantID: 1, Type: types.KnowledgeBaseTypeDocument}
	scope := []*types.KnowledgeBase{
		docKB,
		{ID: "kb-faq", TenantID: 1, Type: types.KnowledgeBaseTypeFAQ},
		{ID: "kb-shared-faq", TenantID: 2, Type: types.KnowledgeBaseTypeFAQ},
	}
	chunks := []*types.IndexWithScore{
		{ChunkID: "own-faq", Score: 0.9},
		{ChunkID: "shared-faq", Score: 0.8},
		{ChunkID: "doc", Score: 0.7},
	}

	out, err := s.applyFAQPostProcessing(newSharedAccessContext(), docKB, scope, chunks, nil, nil,
		types.SearchParams{QueryText: "Refund for gift cards", MatchCount: 10}, 50)
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Equal(t, "doc", out[0].ChunkID)
}

func TestApplyFAQPostProcessingSkipsScopeWithoutFAQ(t *testing.T) {
	s := &knowledgeBaseService{}
	docKB := &types.KnowledgeBase{ID: "kb-doc", Type: types.KnowledgeBaseTypeDocument}
	chunks := []*types.IndexWithScore{{ChunkID: "doc", Score: 0.7}}

	scope := []*types.KnowledgeBase{docKB}
	out, err := s.applyFAQPostProcessing(newSharedAccessContext(), docKB, scope, chunks, nil, nil,
		types.SearchParams{QueryText: "anything", MatchCount: 10}, 50)
	require.NoError(t, err)
	require.Equal(t, chunks, out)
}
