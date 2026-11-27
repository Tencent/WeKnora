package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

func TestWikiExtractionCheckpointIgnoresPagesWrittenBySameOperation(t *testing.T) {
	db, svc, op, ctx := newWikiExtractionCheckpointFixture(t)
	calls := 0
	recipe := map[string]string{"Content": "原文", "PreviousSlugs": "entity/old"}
	extract := func(context.Context, string) (combinedExtraction, error) { calls++; return combinedExtraction{}, nil }
	runner, err := svc.resumableWikiExtraction(ctx, &scriptedTemplateChatModel{}, &op, 1, recipe, extract)
	require.NoError(t, err)
	_, err = runner(ctx, "原文")
	require.NoError(t, err)
	op = reloadWikiExtractionCheckpoint(t, db, svc, op.dbID)
	recipe["PreviousSlugs"] = "entity/old\nentity/new-page-written-before-retry"
	runner, err = svc.resumableWikiExtraction(ctx, &scriptedTemplateChatModel{}, &op, 1, recipe, extract)
	require.NoError(t, err)
	_, err = runner(ctx, "原文")
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.Equal(t, "entity/old", recipe["PreviousSlugs"])
}

type progressChunks struct {
	interfaces.ChunkRepository
	chunks []*types.Chunk
}

func (p progressChunks) ListChunksByKnowledgeIDAndTypes(
	context.Context, uint64, string, []types.ChunkType,
) ([]*types.Chunk, error) {
	return p.chunks, nil
}

func (p progressChunks) ListChunksByParentIDs(context.Context, uint64, []string) ([]*types.Chunk, error) {
	return nil, nil
}

func TestWikiMappedInputAndTaxonomySurviveWorkerRestart(t *testing.T) {
	db, svc, op, ctx := newWikiExtractionCheckpointFixture(t)
	content := strings.Repeat("清代新疆史料，记述西藏的历史。", 20)
	batch := &WikiBatchContext{}
	model := &scriptedTemplateChatModel{}
	svc.knowledgeSvc = &wikiKnowledgeLookupStub{lookup: func(context.Context, string) (*types.Knowledge, error) {
		return &types.Knowledge{ID: op.KnowledgeID, Title: "新疆", ParseStatus: types.ParseStatusFinalizing}, nil
	}}
	svc.chunkRepo = progressChunks{chunks: []*types.Chunk{
		{ID: "chunk", Content: content, ChunkType: types.ChunkTypeText},
	}}
	key := wikiMappedInputKey(content+"\x00新疆", types.ResolveLanguageName(ctx, op.Language), model, batch)
	mapped := &wikiMappedDocument{
		InputKey: key,
		Result: &docIngestResult{
			KnowledgeID: op.KnowledgeID,
			Pages:       []wikiIngestPageRef{{Slug: "entity/xi-zang", Title: "西藏"}},
		},
		Updates:      []SlugUpdate{{Slug: "entity/xi-zang", KnowledgeID: op.KnowledgeID, Type: "entity"}},
		FoldersReady: true,
		Folders:      map[string]string{"entity/xi-zang": "folder"},
	}
	require.NoError(t, svc.saveWikiMappedDocument(ctx, 1, &op, mapped))
	op = reloadWikiExtractionCheckpoint(t, db, svc, op.dbID)
	// The nil model would panic on any generation call. Restart must load the
	// persisted map and directory plan without issuing extraction/summary calls.
	result, updates, err := svc.mapOneDocument(
		ctx, model, WikiIngestPayload{TenantID: 1, KnowledgeBaseID: "kb"}, op, batch)
	require.NoError(t, err)
	require.Equal(t, mapped.Result.Pages, result.Pages)
	require.Equal(t, mapped.Updates, updates)
	folders, err := svc.planResumableWikiFolders(
		ctx, nil, &types.KnowledgeBase{ID: "kb", TenantID: 1},
		map[string][]SlugUpdate{"entity/xi-zang": updates}, "Chinese", []WikiPendingOp{op},
	)
	require.NoError(t, err)
	require.Equal(t, mapped.Folders, folders)
	require.NotEqual(t, key, wikiMappedInputKey(content+"修改", "Chinese", model, batch))
	// A replaced operation cannot be restored by a stale completion.
	require.NoError(t, db.Delete(&types.TaskPendingOp{}, op.dbID).Error)
	require.ErrorContains(t, svc.saveWikiMappedDocument(ctx, 1, &op, mapped), "deleted or changed")
}

type progressEditor struct {
	templateCaptureChatModel
	calls int
}

func (m *progressEditor) GetModelID() string   { return "model" }
func (m *progressEditor) GetModelName() string { return "model" }
func (m *progressEditor) Chat(_ context.Context, _ []chat.Message, _ *chat.ChatOptions) (*types.ChatResponse, error) {
	m.calls++
	if m.calls == 1 {
		return &types.ChatResponse{Content: ""}, nil
	}
	return &types.ChatResponse{Content: "SUMMARY: 西藏相关史实\n西藏的相关历史记述。"}, nil
}

func TestWikiRetryReuses947PagesAndOnlyWritesFailedPage(t *testing.T) {
	batch := &WikiBatchContext{
		OperationKeys: map[string]string{"article": "34080:source"},
		SlugTitleMany: func(context.Context, []string) map[string]string { return nil },
	}
	model := &progressEditor{}
	pages := &retractionVisibilityPages{}
	svc := &wikiIngestService{wikiService: pages, knowledgeSvc: liveKnowledgeStub{}}
	ctx := context.Background()
	for i := 0; i < 947; i++ {
		page := &types.WikiPage{
			Slug: fmt.Sprintf("entity/done-%d", i), Content: "已经完成的正文", SourceRefs: types.StringArray{"article"},
		}
		updates := []SlugUpdate{{Slug: page.Slug, Type: "entity", KnowledgeID: "article"}}
		require.NoError(t, recordWikiPageOperations(page, updates, batch))
		pages.page = page
		_, _, _, _, err := svc.reduceSlugUpdates(ctx, model, "kb", page.Slug, updates, 1, batch, nil)
		require.NoError(t, err)
	}
	require.Zero(t, model.calls)
	require.Nil(t, pages.written)
	page := &types.WikiPage{
		Slug: "entity/xi-zang", Title: "西藏", Content: "原有其他来源内容", SourceRefs: types.StringArray{"other"},
	}
	pages.page = page
	updates := []SlugUpdate{{
		Slug: page.Slug, Type: "entity", KnowledgeID: "article", SourceRef: "article",
		Item: extractedItem{Name: "西藏", Slug: page.Slug, Details: "西藏的相关历史记述。"},
	}}
	_, _, _, _, err := svc.reduceSlugUpdates(ctx, model, "kb", page.Slug, updates, 1, batch, nil)
	require.ErrorContains(t, err, "empty wiki rewrite")
	require.Nil(t, pages.written)
	// The failed write has no receipt and must be attempted again.
	pending, err := pendingWikiPageUpdates(page, updates, batch)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	_, _, _, _, err = svc.reduceSlugUpdates(ctx, model, "kb", page.Slug, updates, 1, batch, nil)
	require.NoError(t, err)
	require.Equal(t, 2, model.calls)
	require.NotNil(t, pages.written)
	// Reloading the committed page simulates a crash before task acknowledgement.
	raw, err := json.Marshal(pages.written)
	require.NoError(t, err)
	var reloaded types.WikiPage
	require.NoError(t, json.Unmarshal(raw, &reloaded))
	pages.page = &reloaded
	_, _, _, _, err = svc.reduceSlugUpdates(ctx, model, "kb", page.Slug, updates, 1, batch, nil)
	require.NoError(t, err)
	require.Equal(t, 2, model.calls)
	newer := &WikiBatchContext{OperationKeys: map[string]string{"article": "34081:new-source"}}
	pending, err = pendingWikiPageUpdates(&reloaded, updates, newer)
	require.NoError(t, err)
	require.Len(t, pending, 1)
}

func TestWikiPageRetryFiltersCompletedContributors(t *testing.T) {
	page := &types.WikiPage{}
	batch := &WikiBatchContext{OperationKeys: map[string]string{"a": "1:source-a", "b": "2:source-b"}}
	done := SlugUpdate{KnowledgeID: "a", Type: "entity"}
	pending := SlugUpdate{KnowledgeID: "b", Type: "entity"}
	require.NoError(t, recordWikiPageOperations(page, []SlugUpdate{done}, batch))
	remaining, err := pendingWikiPageUpdates(page, []SlugUpdate{done, pending}, batch)
	require.NoError(t, err)
	require.Equal(t, []SlugUpdate{pending}, remaining)
}

func TestWikiLegacyCheckpointUpgradeKeepsSavedBlocks(t *testing.T) {
	db, svc, op, ctx := newWikiExtractionCheckpointFixture(t)
	model := &scriptedTemplateChatModel{}
	recipe := map[string]string{"Content": "原文", "PreviousSlugs": "entity/old"}
	layout := []string{wikiExtractionDigest([]byte("原文"))}
	raw, err := json.Marshal(struct {
		Version            int
		ModelID, ModelName string
		Recipe             map[string]string
		Layout             []string
		MaxTokens          int
	}{1, model.GetModelID(), model.GetModelName(), recipe, layout, wikiLLMMaxTokens})
	require.NoError(t, err)
	op.Extraction = &wikiExtractionProgress{
		Version: 1, Fingerprint: wikiExtractionDigest(raw), Total: 1,
		Segments: map[string]json.RawMessage{layout[0]: json.RawMessage(`{"entities":[],"concepts":[]}`)},
	}
	payload, err := json.Marshal(op)
	require.NoError(t, err)
	require.NoError(t, db.Model(&types.TaskPendingOp{}).Where("id = ?", op.dbID).Update("payload", payload).Error)
	op = reloadWikiExtractionCheckpoint(t, db, svc, op.dbID)
	calls := 0
	run, err := svc.resumableWikiExtraction(ctx, model, &op, 1, recipe,
		func(context.Context, string) (combinedExtraction, error) { calls++; return combinedExtraction{}, nil })
	require.NoError(t, err)
	_, err = run(ctx, "原文")
	require.NoError(t, err)
	require.Zero(t, calls)
	require.Equal(t, 2, op.Extraction.Version)
	op = reloadWikiExtractionCheckpoint(t, db, svc, op.dbID)
	recipe["PreviousSlugs"] = "changed-after-old-version-wrote-pages"
	_, err = svc.resumableWikiExtraction(ctx, model, &op, 1, recipe, nil)
	require.ErrorContains(t, err, "preserving saved blocks")
	require.Len(t, op.Extraction.Segments, 1)
}

func TestWikiPageConflictRetryPersistsCompletionReceipt(t *testing.T) {
	svc, store, model := newWikiPageRetryReducer(t)
	store.beforeWrite = func(_ context.Context, _ *types.WikiPage, call int) error {
		if call == 1 {
			return apprepo.ErrWikiPageConflict
		}
		return nil
	}
	batch := svc.newWikiBatchContext("kb-row-guard", nil)
	batch.OperationKeys = map[string]string{"k-row-guard": "operation:source"}
	ctx := context.Background()
	updates := wikiPageRetryUpdates()
	_, _, _, deferred, err := svc.reduceSlugUpdates(ctx, model, "kb-row-guard", "entity/alpha", updates, 7, batch, nil)
	require.NoError(t, err)
	require.False(t, deferred)
	require.Equal(t, 2, store.count("entity/alpha"))
	_, _, _, _, err = svc.reduceSlugUpdates(ctx, model, "kb-row-guard", "entity/alpha", updates, 7, batch, nil)
	require.NoError(t, err)
	require.Len(t, model.captured("wiki_page_modify:entity/alpha"), 2,
		"retry context must preserve the durable operation receipt")
	require.Equal(t, 2, store.count("entity/alpha"))
}
