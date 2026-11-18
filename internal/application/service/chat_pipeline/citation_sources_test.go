package chatpipeline

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestExpandedContextHasSeparateCitationBodies(t *testing.T) {
	a := &types.Chunk{
		ID:              "a",
		KnowledgeID:     "doc",
		KnowledgeBaseID: "kb",
		Content:         "Opening greeting.",
		ParentChunkID:   "p",
		ChunkType:       types.ChunkTypeText,
		IsEnabled:       true,
	}
	b := &types.Chunk{
		ID:              "b",
		KnowledgeID:     "doc",
		KnowledgeBaseID: "kb",
		Content:         "Exact later quotation.",
		ParentChunkID:   "p",
		ChunkType:       types.ChunkTypeText,
		IsEnabled:       true,
		ChunkIndex:      1,
	}
	disabled := &types.Chunk{
		ID:              "disabled",
		KnowledgeID:     "doc",
		KnowledgeBaseID: "kb",
		Content:         "Exact later quotation.",
		ParentChunkID:   "p",
		ChunkType:       types.ChunkTypeText,
	}
	foreign := &types.Chunk{
		ID:              "foreign",
		KnowledgeID:     "other",
		KnowledgeBaseID: "kb",
		Content:         "Opening greeting.",
		IsEnabled:       true,
	}
	repo := &expandChunkRepo{
		chunks: map[string]*types.Chunk{
			"a": a,
			"p": {
				ID:        "p",
				ChunkType: types.ChunkTypeParentText,
			},
		},
		children: map[string][]*types.Chunk{"p": {
			a,
			b,
			disabled,
			foreign,
		}},
	}
	p := &PluginMerge{chunkRepo: repo}
	merged := &types.SearchResult{
		ID:               "a",
		KnowledgeID:      "doc",
		KnowledgeBaseID:  "kb",
		Content:          "Opening greeting.\n\nExact later quotation.",
		ContentRewritten: true,
	}
	p.attachCitationSources(context.Background(), []*types.SearchResult{merged})
	got := expandCitationSources([]*types.SearchResult{merged, merged})
	require.Len(t, got, 2)
	require.Equal(t, "a", got[0].ID)
	require.Equal(t, a.Content, got[0].Content)
	require.Equal(t, "b", got[1].ID)
	require.Equal(t, b.Content, got[1].Content)
	require.Empty(t, got[0].SubChunkID)
}

func TestExpandedEvidenceFailsClosedAndPreservesPlainSources(t *testing.T) {
	got := expandCitationSources([]*types.SearchResult{
		{
			ID:               "expanded",
			Content:          "parent text under child ID",
			ContentRewritten: true,
		},
		{
			ID:      "plain",
			Content: "actual source",
		},
	})
	require.Len(t, got, 1)
	require.Equal(t, "plain", got[0].ID)
}

// Merge scoping keeps a sibling's image Markdown out of the merged Content and
// ImageInfo, yet the prompt renders the sibling's raw citation body, so an
// answer may cite that image. The handle must therefore survive in the
// persisted reference (what the message-file proxy authorizes from), not only
// in the in-memory citation sources (#5069).
func TestCitationSourceImageHandleSurvivesReferencePersistence(t *testing.T) {
	const siblingHandle = "SiBlInGpIcTuRe00000000"
	hit := &types.Chunk{
		ID:              "hit",
		KnowledgeID:     "doc",
		KnowledgeBaseID: "kb",
		Content:         "Hit body.",
		ParentChunkID:   "p",
		ChunkType:       types.ChunkTypeText,
		IsEnabled:       true,
	}
	sibling := &types.Chunk{
		ID:              "sibling",
		KnowledgeID:     "doc",
		KnowledgeBaseID: "kb",
		Content:         "Sibling body\n\n![diagram](resource://" + siblingHandle + ")",
		ParentChunkID:   "p",
		ChunkType:       types.ChunkTypeText,
		IsEnabled:       true,
		ChunkIndex:      1,
	}
	repo := &expandChunkRepo{
		chunks: map[string]*types.Chunk{
			"hit": hit,
			"p":   {ID: "p", ChunkType: types.ChunkTypeParentText},
		},
		children: map[string][]*types.Chunk{"p": {hit, sibling}},
	}
	p := &PluginMerge{chunkRepo: repo}
	// The merged result mirrors resolveParentChunks' output for a text hit:
	// parent content pruned of the sibling's image Markdown, ImageInfo scoped
	// to the matched child (empty here), ContentRewritten set.
	merged := &types.SearchResult{
		ID:               "hit",
		KnowledgeID:      "doc",
		KnowledgeBaseID:  "kb",
		Content:          "Hit body.\n\nSibling body",
		ContentRewritten: true,
	}
	p.attachCitationSources(context.Background(), []*types.SearchResult{merged})

	exposed := false
	for _, source := range merged.CitationSources {
		if source.ID == "sibling" {
			exposed = strings.Contains(source.Content, types.BuildResourcePath(siblingHandle))
		}
	}
	require.True(t, exposed, "scenario must expose the sibling image body to the model")

	raw, err := json.Marshal(types.References{merged})
	require.NoError(t, err)
	require.Contains(t, string(raw), types.BuildResourcePath(siblingHandle))
}

func TestModelHandlesReferenceTheActualExpandedChild(t *testing.T) {
	a := &types.SearchResult{
		ID:              "opening",
		KnowledgeID:     "doc",
		KnowledgeBaseID: "kb",
		KnowledgeTitle:  "Report",
		Content:         "Opening greeting.",
	}
	b := &types.SearchResult{
		ID:              "later",
		KnowledgeID:     "doc",
		KnowledgeBaseID: "kb",
		KnowledgeTitle:  "Report",
		ChunkIndex:      1,
		Content:         "Exact later quotation.",
	}
	rendered := "old expanded context"
	manage := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			Query:         "question",
			SummaryConfig: types.SummaryConfig{Prompt: "system"},
		},
		PipelineState: types.PipelineState{
			RenderedContexts: rendered,
			UserContent:      rendered,
			MergeResult: []*types.SearchResult{{
				ID:               "opening",
				ContentRewritten: true,
				CitationSources: []*types.SearchResult{
					a,
					b,
				},
			}},
		},
	}
	messages, registry := prepareMessagesWithModelContext(context.Background(), manage)
	require.Contains(t, messages[1].Content, `<chunk id="chunk://2"`)
	require.Contains(t, registry.DecodeOutputText(`<ref id="chunk://2"/>`), `chunk_id="later"`)
	require.Contains(t, registry.DecodeOutputText(`<ref id="chunk://1"/>`), `chunk_id="opening"`)
	manage.MergeResult[0].CitationSources = nil
	messages, _ = prepareMessagesWithModelContext(context.Background(), manage)
	require.NotContains(t, messages[1].Content, rendered)
	require.Contains(t, messages[1].Content, "could not be verified")
}
