package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// buildTextChunkWithQuestions creates a text Chunk with embedded GeneratedQuestions.
func buildTextChunkWithQuestions(id string, questions []types.GeneratedQuestion) *types.Chunk {
	chunk := &types.Chunk{
		ID:        id,
		ChunkType: types.ChunkTypeText,
	}
	if len(questions) > 0 {
		meta := &types.DocumentChunkMetadata{GeneratedQuestions: questions}
		_ = chunk.SetDocumentMetadata(meta)
	}
	return chunk
}

func TestAggregateGeneratedQuestions_ReturnsQuestionsFromTextChunks(t *testing.T) {
	chunks := []*types.Chunk{
		buildTextChunkWithQuestions("chunk-1", []types.GeneratedQuestion{
			{ID: "q1", Question: "What is the return policy?"},
			{ID: "q2", Question: "How long does shipping take?"},
		}),
	}

	got := aggregateGeneratedQuestions(chunks)

	require.Len(t, got, 2)
	require.Equal(t, "chunk-1", got[0].ChunkID)
	require.Equal(t, "q1", got[0].QuestionID)
	require.Equal(t, "What is the return policy?", got[0].Question)
	require.Equal(t, "q2", got[1].QuestionID)
}

func TestAggregateGeneratedQuestions_SkipsNonTextChunks(t *testing.T) {
	textChunk := buildTextChunkWithQuestions("chunk-text", []types.GeneratedQuestion{
		{ID: "q1", Question: "How do I contact support?"},
	})
	faqChunk := &types.Chunk{
		ID:        "chunk-faq",
		ChunkType: types.ChunkTypeFAQ,
	}
	_ = faqChunk.SetDocumentMetadata(&types.DocumentChunkMetadata{
		GeneratedQuestions: []types.GeneratedQuestion{{ID: "q99", Question: "Should be skipped"}},
	})

	got := aggregateGeneratedQuestions([]*types.Chunk{textChunk, faqChunk})

	require.Len(t, got, 1)
	require.Equal(t, "chunk-text", got[0].ChunkID)
}

func TestAggregateGeneratedQuestions_EmptyWhenNoQuestionsGenerated(t *testing.T) {
	chunks := []*types.Chunk{
		buildTextChunkWithQuestions("chunk-1", nil),
	}

	got := aggregateGeneratedQuestions(chunks)

	require.Empty(t, got)
}

func TestAggregateGeneratedQuestions_SkipsEmptyQuestionText(t *testing.T) {
	chunks := []*types.Chunk{
		buildTextChunkWithQuestions("chunk-1", []types.GeneratedQuestion{
			{ID: "q1", Question: "Valid question"},
			{ID: "q2", Question: ""},
		}),
	}

	got := aggregateGeneratedQuestions(chunks)

	require.Len(t, got, 1)
	require.Equal(t, "Valid question", got[0].Question)
}

func TestAggregateGeneratedQuestions_MultipleChunks(t *testing.T) {
	chunks := []*types.Chunk{
		buildTextChunkWithQuestions("chunk-1", []types.GeneratedQuestion{
			{ID: "q1", Question: "First chunk question"},
		}),
		buildTextChunkWithQuestions("chunk-2", []types.GeneratedQuestion{
			{ID: "q2", Question: "Second chunk question"},
			{ID: "q3", Question: "Another second chunk question"},
		}),
	}

	got := aggregateGeneratedQuestions(chunks)

	require.Len(t, got, 3)
	require.Equal(t, "chunk-1", got[0].ChunkID)
	require.Equal(t, "chunk-2", got[1].ChunkID)
	require.Equal(t, "chunk-2", got[2].ChunkID)
}
