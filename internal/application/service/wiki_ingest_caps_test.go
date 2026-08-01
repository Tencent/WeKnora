package service

import (
	"context"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

// capWikiServiceStub records the pages handed to UpdatePage/CreatePage so
// tests can assert what reduce persisted (and, crucially, whether the page
// body was rewritten).
type capWikiServiceStub struct {
	interfaces.WikiPageService
	existing     *types.WikiPage
	updatedPages []*types.WikiPage
	createdPages []*types.WikiPage
}

func (s *capWikiServiceStub) GetPageBySlug(
	_ context.Context, kbID, slug string,
) (*types.WikiPage, error) {
	if s.existing != nil && s.existing.KnowledgeBaseID == kbID && s.existing.Slug == slug {
		cp := *s.existing
		return &cp, nil
	}
	return nil, nil
}

func (s *capWikiServiceStub) UpdatePage(_ context.Context, page *types.WikiPage) (*types.WikiPage, error) {
	cp := *page
	s.updatedPages = append(s.updatedPages, &cp)
	return page, nil
}

func (s *capWikiServiceStub) CreatePage(_ context.Context, page *types.WikiPage) (*types.WikiPage, error) {
	cp := *page
	s.createdPages = append(s.createdPages, &cp)
	return page, nil
}

// capKnowledgeServiceStub reports every knowledge id as alive so
// filterLiveUpdates keeps the test's addition updates.
type capKnowledgeServiceStub struct {
	interfaces.KnowledgeService
}

func (s *capKnowledgeServiceStub) GetKnowledgeByIDOnly(
	_ context.Context, id string,
) (*types.Knowledge, error) {
	return &types.Knowledge{ID: id, ParseStatus: types.ParseStatusCompleted}, nil
}

// capChunkRepoStub resolves the cited chunks fed to the reduce prompt.
type capChunkRepoStub struct {
	interfaces.ChunkRepository
	contents map[string]string
}

func (s *capChunkRepoStub) ListChunksByID(
	_ context.Context, _ uint64, ids []string,
) ([]*types.Chunk, error) {
	out := make([]*types.Chunk, 0, len(ids))
	for _, id := range ids {
		if content, ok := s.contents[id]; ok {
			out = append(out, &types.Chunk{ID: id, Content: content})
		}
	}
	return out, nil
}

// countingChatModel counts Chat invocations so tests can prove the
// re-synthesis LLM call was skipped (or made).
type countingChatModel struct {
	calls    int
	response string
}

func (m *countingChatModel) Chat(
	_ context.Context, _ []chat.Message, _ *chat.ChatOptions,
) (*types.ChatResponse, error) {
	m.calls++
	return &types.ChatResponse{Content: m.response}, nil
}

func (m *countingChatModel) ChatStream(
	context.Context, []chat.Message, *chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	return nil, nil
}

func (m *countingChatModel) GetModelName() string { return "counting" }
func (m *countingChatModel) GetModelID() string   { return "counting" }

func capTestBatchCtx(maxPageContentBytes, maxRefs int) *WikiBatchContext {
	return &WikiBatchContext{
		SlugTitleMany: func(context.Context, []string) map[string]string { return nil },
		SummaryContentByKnowledgeID: func(context.Context, string) string {
			return ""
		},
		PlannedFolderID:     map[string]string{},
		MaxPageContentBytes: maxPageContentBytes,
		MaxRefs:             maxRefs,
	}
}

func capTestAddition(kid string, chunkIDs ...string) SlugUpdate {
	return SlugUpdate{
		Slug:         "hub-page",
		Type:         types.WikiPageTypeEntity,
		Item:         extractedItem{Name: "Hub Page", Description: "desc", Details: "details"},
		DocTitle:     "New Report",
		KnowledgeID:  kid,
		SourceRef:    kid + "|New Report",
		Language:     "zh-CN",
		SourceChunks: chunkIDs,
	}
}

func existingHubPage(content string, chunkRefs ...string) *types.WikiPage {
	return &types.WikiPage{
		ID:              "page-1",
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		Slug:            "hub-page",
		Title:           "Hub Page",
		Content:         content,
		PageType:        types.WikiPageTypeEntity,
		Status:          types.WikiPageStatusPublished,
		SourceRefs:      types.StringArray{"old-kid|Old Report"},
		ChunkRefs:       chunkRefs,
	}
}

func TestReduceSlugUpdatesContentCapSkipsResynthesis(t *testing.T) {
	body := strings.Repeat("x", 4096)
	wikiSvc := &capWikiServiceStub{existing: existingHubPage(body, "c1")}
	model := &countingChatModel{response: "SUMMARY: should-not-be-used\nrewritten"}
	svc := &wikiIngestService{
		wikiService:  wikiSvc,
		knowledgeSvc: &capKnowledgeServiceStub{},
		chunkRepo:    &capChunkRepoStub{contents: map[string]string{"c2": "chunk two"}},
	}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))

	changed, affectedType, additionFailed, err := svc.reduceSlugUpdates(
		ctx, model, "kb-1", "hub-page",
		[]SlugUpdate{capTestAddition("new-kid", "c2")},
		1, capTestBatchCtx(1024, 0), nil,
	)
	require.NoError(t, err)
	require.True(t, changed, "bookkeeping-only persist must still run")
	require.Equal(t, "ingest", affectedType)
	require.False(t, additionFailed)
	require.Zero(t, model.calls, "capped add-only update must skip the LLM re-synthesis")

	require.Len(t, wikiSvc.updatedPages, 1)
	require.Empty(t, wikiSvc.createdPages)
	persisted := wikiSvc.updatedPages[0]
	require.Equal(t, body, persisted.Content,
		"capped page body must be preserved so UpdatePage routes to the UpdateMeta (no fulltext GIN) path")
	require.Contains(t, []string(persisted.SourceRefs), "new-kid|New Report",
		"fresh source refs must still land for delete reconciliation")
	require.Equal(t, types.StringArray{"c1", "c2"}, persisted.ChunkRefs,
		"freshly-cited chunk refs must still land for retrieval grounding")
}

func TestReduceSlugUpdatesContentCapBelowThresholdStillSynthesizes(t *testing.T) {
	wikiSvc := &capWikiServiceStub{existing: existingHubPage(strings.Repeat("x", 512))}
	model := &countingChatModel{response: "SUMMARY: refreshed\nregenerated body"}
	svc := &wikiIngestService{
		wikiService:  wikiSvc,
		knowledgeSvc: &capKnowledgeServiceStub{},
		chunkRepo:    &capChunkRepoStub{contents: map[string]string{"c2": "chunk two"}},
	}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))

	changed, _, _, err := svc.reduceSlugUpdates(
		ctx, model, "kb-1", "hub-page",
		[]SlugUpdate{capTestAddition("new-kid", "c2")},
		1, capTestBatchCtx(1024, 0), nil,
	)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, 1, model.calls, "page below the cap must still be re-synthesized")
	require.Len(t, wikiSvc.updatedPages, 1)
	require.Equal(t, "regenerated body", wikiSvc.updatedPages[0].Content)
}

func TestReduceSlugUpdatesContentCapNeverAppliesToRetract(t *testing.T) {
	page := existingHubPage(strings.Repeat("x", 4096), "c1")
	page.SourceRefs = types.StringArray{"dead-kid|Dead Report", "keep-kid|Keep Report"}
	wikiSvc := &capWikiServiceStub{existing: page}
	model := &countingChatModel{response: "SUMMARY: after-retract\nremaining body"}
	svc := &wikiIngestService{
		wikiService:  wikiSvc,
		knowledgeSvc: &capKnowledgeServiceStub{},
		chunkRepo:    &capChunkRepoStub{},
	}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))

	retract := SlugUpdate{
		Slug:              "hub-page",
		Type:              "retract",
		DocTitle:          "Dead Report",
		KnowledgeID:       "dead-kid",
		Language:          "zh-CN",
		RetractDocContent: "outdated content",
	}
	changed, affectedType, _, err := svc.reduceSlugUpdates(
		ctx, model, "kb-1", "hub-page",
		[]SlugUpdate{retract},
		1, capTestBatchCtx(1024, 0), nil,
	)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "retract", affectedType)
	require.Equal(t, 1, model.calls, "retractions are never capped: the page must be regenerated")
	require.Len(t, wikiSvc.updatedPages, 1)
	require.Equal(t, "remaining body", wikiSvc.updatedPages[0].Content)
	require.Equal(t, types.StringArray{"keep-kid|Keep Report"}, wikiSvc.updatedPages[0].SourceRefs)
}

func TestReduceSlugUpdatesMaxRefsTrimsChunkRefs(t *testing.T) {
	wikiSvc := &capWikiServiceStub{
		existing: existingHubPage(strings.Repeat("x", 4096), "c1", "c2"),
	}
	model := &countingChatModel{response: "SUMMARY: unused\nunused"}
	svc := &wikiIngestService{
		wikiService:  wikiSvc,
		knowledgeSvc: &capKnowledgeServiceStub{},
		chunkRepo:    &capChunkRepoStub{contents: map[string]string{"c3": "chunk three"}},
	}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))

	changed, _, _, err := svc.reduceSlugUpdates(
		ctx, model, "kb-1", "hub-page",
		[]SlugUpdate{capTestAddition("new-kid", "c3")},
		1, capTestBatchCtx(1024, 2), nil,
	)
	require.NoError(t, err)
	require.True(t, changed)
	require.Len(t, wikiSvc.updatedPages, 1)
	require.Equal(t, types.StringArray{"c2", "c3"}, wikiSvc.updatedPages[0].ChunkRefs,
		"chunk_refs must be trimmed to the most-recent MaxRefs entries")
}

func TestReduceSlugUpdatesZeroCapsPreserveHistoricalBehavior(t *testing.T) {
	wikiSvc := &capWikiServiceStub{
		existing: existingHubPage(strings.Repeat("x", 4096), "c1"),
	}
	model := &countingChatModel{response: "SUMMARY: refreshed\nregenerated body"}
	svc := &wikiIngestService{
		wikiService:  wikiSvc,
		knowledgeSvc: &capKnowledgeServiceStub{},
		chunkRepo:    &capChunkRepoStub{contents: map[string]string{"c2": "chunk two"}},
	}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))

	changed, _, _, err := svc.reduceSlugUpdates(
		ctx, model, "kb-1", "hub-page",
		[]SlugUpdate{capTestAddition("new-kid", "c2")},
		1, capTestBatchCtx(0, 0), nil,
	)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, 1, model.calls, "0 = disabled: oversized pages keep the historical re-synthesis path")
	require.Len(t, wikiSvc.updatedPages, 1)
	require.Equal(t, "regenerated body", wikiSvc.updatedPages[0].Content)
	require.Equal(t, types.StringArray{"c1", "c2"}, wikiSvc.updatedPages[0].ChunkRefs,
		"MaxRefs = 0 leaves chunk_refs unbounded")
}

func TestCapRecentStringArray(t *testing.T) {
	require.Nil(t, capRecentStringArray(nil, 3))
	require.Equal(t, types.StringArray{"a", "b"}, capRecentStringArray(types.StringArray{"a", "b"}, 3),
		"within bounds stays unchanged")
	require.Equal(t, types.StringArray{"a", "b"}, capRecentStringArray(types.StringArray{"a", "b"}, 0),
		"max <= 0 disables the cap")
	require.Equal(t, types.StringArray{"b", "c"}, capRecentStringArray(types.StringArray{"a", "b", "c"}, 2),
		"over bounds keeps the most-recent tail")
}
