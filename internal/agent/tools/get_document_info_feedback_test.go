package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type documentInfoFeedbackChunkService struct {
	interfaces.ChunkService
	chunk *types.Chunk
}

func (s *documentInfoFeedbackChunkService) GetChunkByIDOnly(_ context.Context, id string) (*types.Chunk, error) {
	if s.chunk != nil && s.chunk.ID == id {
		return s.chunk, nil
	}
	return nil, nil
}

func TestGetDocumentInfoFAQCarriesRuntimeKnowledgeReference(t *testing.T) {
	chunk := &types.Chunk{
		ID:              "faq-1",
		KnowledgeID:     "document-1",
		KnowledgeBaseID: "kb-1",
		ChunkType:       types.ChunkTypeFAQ,
		IsEnabled:       true,
		RecallWeight:    0.5,
	}
	require.NoError(t, chunk.SetFAQMetadata(&types.FAQChunkMetadata{
		StandardQuestion: "What is the policy?",
		Answers:          []string{"The policy answer."},
		SimilarQuestions: []string{"How does the policy work?"},
	}))
	tool := NewGetDocumentInfoTool(
		nil,
		&documentInfoFeedbackChunkService{chunk: chunk},
		types.SearchTargets{{
			Type:            types.SearchTargetTypeKnowledgeBase,
			KnowledgeBaseID: "kb-1",
			TenantID:        7,
		}},
	)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"faq_ids":["faq-1"]}`))

	require.NoError(t, err)
	require.True(t, result.Success)
	require.Contains(t, result.Output, "The policy answer.")
	require.Len(t, result.KnowledgeReferences, 1)
	require.Equal(t, "faq-1", result.KnowledgeReferences[0].ID)
	require.Equal(t, "kb-1", result.KnowledgeReferences[0].KnowledgeBaseID)
	require.Equal(t, types.MatchTypeKeywords, result.KnowledgeReferences[0].MatchType)
	require.Equal(t, 0.5, result.KnowledgeReferences[0].RecallWeight)
}
