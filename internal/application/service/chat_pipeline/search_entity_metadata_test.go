package chatpipeline

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestChunk2SearchResultIncludesCustomMetadata(t *testing.T) {
	t.Parallel()
	knowledge := &types.Knowledge{
		Title:           "Course",
		Description:     "desc",
		FileName:        "lesson.md",
		Source:          "api",
		Channel:         "web",
		KnowledgeBaseID: "kb-1",
		CustomMetadata: types.JSON(`{
			"lesson_url":"https://example.com/lesson",
			"course_url":"https://example.com/course"
		}`),
	}
	chunk := &types.Chunk{
		ID:          "chunk-1",
		KnowledgeID: "knowledge-1",
		Content:     "body",
		ChunkType:   types.ChunkTypeText,
	}

	got := chunk2SearchResult(chunk, knowledge)

	require.Equal(t,
		"course_url: https://example.com/course\nlesson_url: https://example.com/lesson",
		got.KnowledgeCustomMetadata,
	)
	require.Equal(t, "desc", got.KnowledgeDescription)
	require.Equal(t, "web", got.KnowledgeChannel)
	require.Equal(t, 0.0, got.Score)
}
