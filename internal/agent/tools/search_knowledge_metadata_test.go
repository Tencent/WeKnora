package tools

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestWriteKnowledgeMetadataHeaderDeduplicatesByKnowledge(t *testing.T) {
	t.Parallel()
	results := []*searchResultWithMeta{
		{
			SearchResult: &types.SearchResult{
				KnowledgeID: "knowledge-1", KnowledgeTitle: "Document <One>",
				KnowledgeCustomMetadata: "region: Shanghai & Suzhou",
			},
			KnowledgeBaseID: "kb-1",
		},
		{
			SearchResult: &types.SearchResult{
				KnowledgeID: "knowledge-1", KnowledgeTitle: "Document <One>",
				KnowledgeCustomMetadata: "region: Shanghai & Suzhou",
			},
			KnowledgeBaseID: "kb-1",
		},
		{
			SearchResult: &types.SearchResult{
				KnowledgeID: "knowledge-2", KnowledgeTitle: "Document Two",
			},
			KnowledgeBaseID: "kb-1",
		},
		{
			SearchResult: &types.SearchResult{
				KnowledgeID: "knowledge-3", KnowledgeTitle: "Document Three",
				KnowledgeCustomMetadata: "owner: Alice",
			},
			KnowledgeBaseID: "kb-2",
		},
	}

	var output strings.Builder
	writeKnowledgeMetadataHeader(&output, results)
	text := output.String()

	require.Equal(t, 1, strings.Count(text, `knowledge_id="knowledge-1"`))
	require.Equal(t, 1, strings.Count(text, `knowledge_id="knowledge-3"`))
	require.NotContains(t, text, `knowledge_id="knowledge-2"`)
	require.Equal(t, 2, strings.Count(text, "<metadata>"))
	require.Contains(t, text, "Document &lt;One&gt;")
	require.Contains(t, text, "Shanghai &amp; Suzhou")
}

func TestWriteKnowledgeMetadataHeaderOmitsEmptySection(t *testing.T) {
	t.Parallel()
	var output strings.Builder
	writeKnowledgeMetadataHeader(&output, []*searchResultWithMeta{{
		SearchResult: &types.SearchResult{KnowledgeID: "knowledge-1"},
	}})
	require.Empty(t, output.String())
}
