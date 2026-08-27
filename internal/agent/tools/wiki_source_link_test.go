package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type sourceLinkKnowledgeService struct {
	interfaces.KnowledgeService
	knowledge     *types.Knowledge
	knowledgeByID map[string]*types.Knowledge
	requested     []string
}

func (s *sourceLinkKnowledgeService) GetKnowledgeByIDOnly(_ context.Context, id string) (*types.Knowledge, error) {
	s.requested = append(s.requested, id)
	if s.knowledgeByID != nil {
		return s.knowledgeByID[id], nil
	}
	return s.knowledge, nil
}

type sourceLinkChunkRepository struct {
	interfaces.ChunkRepository
	chunks []*types.Chunk
}

func (r *sourceLinkChunkRepository) ListPagedChunksByKnowledgeID(
	_ context.Context,
	_ uint64,
	_ string,
	_ *types.Pagination,
	_ []types.ChunkType,
	_ []string,
	_, _, _, _ string,
	_ *bool,
) ([]*types.Chunk, int64, error) {
	return r.chunks, int64(len(r.chunks)), nil
}

func (r *sourceLinkChunkRepository) ListChunksByParentIDs(
	context.Context, uint64, []string,
) ([]*types.Chunk, error) {
	return nil, nil
}

type sourceLinkChunkService struct {
	interfaces.ChunkService
	repository interfaces.ChunkRepository
}

func (s *sourceLinkChunkService) GetRepository() interfaces.ChunkRepository {
	return s.repository
}

func TestWikiReadPageDisabledSourceLinksKeepModelSourcesButHideClientMetadata(t *testing.T) {
	page := newTestWikiPage("kb-1", "concept/source-links")
	page.SourceRefs = []string{"doc-1|Source paper.pdf"}
	service := &fakeWikiPageService{pages: map[string]*types.WikiPage{
		wikiPageKey("kb-1", page.Slug): page,
	}}
	tool := NewWikiReadPageTool(
		service,
		nil,
		NewWikiScopesFromKBIDs([]string{"kb-1"}),
		NewWikiRouteResolver(),
	).WithSourceLinks(false, types.SearchTargets{{
		Type:            types.SearchTargetTypeKnowledgeBase,
		KnowledgeBaseID: "kb-1",
	}})

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"slugs":["concept/source-links"]}`))
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Success, result.Error)

	assert.Contains(t, result.Output, `<source knowledge_id="doc-1">Source paper.pdf</source>`,
		"the in-memory model result must retain sources")
	assert.Equal(t, false, result.Data["preview_enabled"])
	_, hasSourceDocuments := result.Data["source_documents"]
	assert.False(t, hasSourceDocuments, "disabled result must not expose client source-document metadata")

	client := SanitizeToolResultForClient(ToolWikiReadPage, result)
	assert.Contains(t, client["output"], "<sources>")
	assert.Contains(t, client["output"], "Source paper.pdf")
	assert.NotContains(t, client["output"], "knowledge_id")
	assert.NotContains(t, client["output"], "doc-1")
}

func TestWikiReadPageEnabledSourceLinksReturnOnlyRenderedAuthorizedDocuments(t *testing.T) {
	first := newBulkyWikiPage("kb-1", "concept/first", 4000)
	first.SourceRefs = []string{"doc-1|stale title", "doc-1|duplicate", "doc-disabled|Disabled.pdf"}
	omitted := newBulkyWikiPage("kb-1", "concept/omitted", 4000)
	omitted.SourceRefs = []string{"doc-omitted|Omitted.pdf"}
	wikiService := &fakeWikiPageService{pages: map[string]*types.WikiPage{
		wikiPageKey("kb-1", first.Slug):   first,
		wikiPageKey("kb-1", omitted.Slug): omitted,
	}}
	knowledgeService := &sourceLinkKnowledgeService{knowledgeByID: map[string]*types.Knowledge{
		"doc-1": {
			ID: "doc-1", KnowledgeBaseID: "kb-1", Title: "Verified.pdf",
			FileType: "pdf", ParseStatus: types.ParseStatusCompleted,
		},
		"doc-disabled": {
			ID: "doc-disabled", KnowledgeBaseID: "kb-1", Title: "Disabled.pdf",
			EnableStatus: "disabled", ParseStatus: types.ParseStatusCompleted,
		},
		"doc-omitted": {
			ID: "doc-omitted", KnowledgeBaseID: "kb-1", Title: "Omitted.pdf",
			ParseStatus: types.ParseStatusCompleted,
		},
	}}
	tool := NewWikiReadPageTool(
		wikiService,
		knowledgeService,
		NewWikiScopesFromKBIDs([]string{"kb-1"}),
		NewWikiRouteResolver(),
	).WithSourceLinks(true, types.SearchTargets{{
		Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "kb-1",
	}})

	result := readPageOutput(t, WithOutputBudget(context.Background(), 1000), tool, []string{first.Slug, omitted.Slug})
	require.NotEmpty(t, result.Data["omitted_slugs"])
	assert.Equal(t, true, result.Data["preview_enabled"])
	assert.NotContains(t, knowledgeService.requested, "doc-omitted",
		"sources for pages omitted by the output budget must not be resolved")

	pages, ok := result.Data["source_documents"].([]map[string]interface{})
	require.True(t, ok)
	require.Len(t, pages, 1)
	assert.Equal(t, first.Slug, pages[0]["slug"])
	docs, ok := pages[0]["source_documents"].([]map[string]interface{})
	require.True(t, ok)
	require.Len(t, docs, 1)
	assert.Equal(t, map[string]interface{}{
		"knowledge_id": "doc-1", "knowledge_base_id": "kb-1",
		"title": "Verified.pdf", "file_type": "pdf", "preview_enabled": true,
	}, docs[0])
}

func TestWikiReadSourceDocDisabledSourceLinkKeepsContentButHidesClientIdentity(t *testing.T) {
	knowledge := &types.Knowledge{
		ID:              "doc-1",
		TenantID:        10001,
		KnowledgeBaseID: "kb-1",
		Title:           "Source paper.pdf",
		FileType:        "pdf",
	}
	repository := &sourceLinkChunkRepository{chunks: []*types.Chunk{{
		ID:              "chunk-1",
		KnowledgeID:     knowledge.ID,
		KnowledgeBaseID: knowledge.KnowledgeBaseID,
		ChunkIndex:      0,
		ChunkType:       types.ChunkTypeText,
		Content:         "Verified source content.",
	}}}
	tool := NewWikiReadSourceDocTool(
		&sourceLinkKnowledgeService{knowledge: knowledge},
		&sourceLinkChunkService{repository: repository},
	).WithSourceLink(false)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"knowledge_id":"doc-1"}`))
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Success, result.Error)

	assert.Contains(t, result.Output, "Verified source content.")
	assert.Equal(t, false, result.Data["preview_enabled"])
	for _, key := range []string{"knowledge_id", "knowledge_base_id", "file_type"} {
		_, exists := result.Data[key]
		assert.False(t, exists, "disabled result must not expose %s", key)
	}

	chunks, ok := result.Data["chunks"].([]map[string]interface{})
	require.True(t, ok)
	require.Len(t, chunks, 1)
	assert.Equal(t, map[string]interface{}{
		"chunk_id":    "chunk-1",
		"chunk_index": 0,
		"chunk_type":  types.ChunkTypeText,
		"content":     "Verified source content.",
	}, chunks[0])
}

func TestWikiReadSourceDocEnabledSourceLinkReturnsExplicitPreviewCapability(t *testing.T) {
	knowledge := &types.Knowledge{
		ID:              "doc-1",
		TenantID:        10001,
		KnowledgeBaseID: "kb-1",
		Title:           "Source paper.pdf",
		FileType:        "pdf",
	}
	tool := NewWikiReadSourceDocTool(
		&sourceLinkKnowledgeService{knowledge: knowledge},
		&sourceLinkChunkService{repository: &sourceLinkChunkRepository{}},
	).WithSourceLink(true)

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"knowledge_id":"doc-1"}`))
	require.NoError(t, err)
	require.True(t, result.Success, result.Error)
	assert.Equal(t, true, result.Data["preview_enabled"])
	assert.Equal(t, "doc-1", result.Data["knowledge_id"])
	assert.Equal(t, "kb-1", result.Data["knowledge_base_id"])
	assert.Equal(t, "pdf", result.Data["file_type"])
	assert.NotContains(t, result.Data, "original_file_path")
}
