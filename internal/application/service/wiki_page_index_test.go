package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type wikiIndexKB struct {
	interfaces.KnowledgeBaseService
	kb *types.KnowledgeBase
}

func (s *wikiIndexKB) GetKnowledgeBaseByIDOnly(context.Context, string) (*types.KnowledgeBase, error) {
	return s.kb, nil
}

type wikiIndexTenant struct{ interfaces.TenantService }

func (wikiIndexTenant) GetTenantByID(_ context.Context, id uint64) (*types.Tenant, error) {
	return &types.Tenant{ID: id, RetrieverEngines: types.RetrieverEngines{
		Engines: []types.RetrieverEngineParams{{
			RetrieverType: types.VectorRetrieverType, RetrieverEngineType: types.PostgresRetrieverEngineType,
		}},
	}}, nil
}

type wikiIndexEngine struct {
	interfaces.RetrieveEngineService
	items     map[string]*types.IndexInfo
	err       error
	deleteErr error
}

func (*wikiIndexEngine) EngineType() types.RetrieverEngineType {
	return types.PostgresRetrieverEngineType
}

func (*wikiIndexEngine) Support() []types.RetrieverType {
	return []types.RetrieverType{types.VectorRetrieverType, types.KeywordsRetrieverType}
}

func (e *wikiIndexEngine) BatchIndex(
	ctx context.Context, embedder embedding.Embedder, infos []*types.IndexInfo, _ []types.RetrieverType,
) error {
	if e.err != nil {
		return e.err
	}
	texts := make([]string, 0, len(infos))
	for _, info := range infos {
		texts = append(texts, info.Content)
	}
	if _, err := embedder.BatchEmbedWithPool(ctx, embedder, texts); err != nil {
		return err
	}
	for _, info := range infos {
		e.items[info.ChunkID] = info
	}
	return nil
}

func (e *wikiIndexEngine) DeleteByKnowledgeIDList(_ context.Context, ids []string, _ int, _ string) error {
	if e.deleteErr != nil {
		return e.deleteErr
	}
	for _, id := range ids {
		for chunkID, info := range e.items {
			if info.KnowledgeID == id {
				delete(e.items, chunkID)
			}
		}
	}
	return nil
}

type wikiIndexRegistry struct {
	interfaces.RetrieveEngineRegistry
	engine *wikiIndexEngine
}

func (r wikiIndexRegistry) GetRetrieveEngineService(
	types.RetrieverEngineType,
) (interfaces.RetrieveEngineService, error) {
	return r.engine, nil
}

type wikiIndexEmbedder struct {
	embedding.Embedder
	texts []string
}

func (*wikiIndexEmbedder) GetDimensions() int { return 1 }
func (e *wikiIndexEmbedder) BatchEmbedWithPool(
	_ context.Context, _ embedding.Embedder, texts []string,
) ([][]float32, error) {
	e.texts = append(e.texts, texts...)
	return make([][]float32, len(texts)), nil
}

type wikiIndexModel struct {
	interfaces.ModelService
	embedder *wikiIndexEmbedder
	err      error
	tenantID uint64
}

func (m *wikiIndexModel) GetEmbeddingModel(ctx context.Context, _ string) (embedding.Embedder, error) {
	m.tenantID = types.MustTenantIDFromContext(ctx)
	return m.embedder, m.err
}

type wikiIndexKnowledgeRepo struct{ interfaces.KnowledgeRepository }

func (wikiIndexKnowledgeRepo) GetKnowledgeBatch(context.Context, uint64, []string) ([]*types.Knowledge, error) {
	return nil, nil
}

func (wikiIndexKnowledgeRepo) GetKnowledgeBatchByIDOnly(context.Context, []string) ([]*types.Knowledge, error) {
	return nil, nil
}

func newWikiIndexFixture(t *testing.T) (
	context.Context, *wikiPageService, interfaces.ChunkRepository, *wikiIndexEngine, *wikiIndexEmbedder,
) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.WikiPage{}, &types.WikiPageRevision{}, &types.Chunk{}))
	kb := &types.KnowledgeBase{
		ID: "kb-wiki", TenantID: 7, Type: "document", EmbeddingModelID: "embed",
		IndexingStrategy: types.IndexingStrategy{WikiEnabled: true, VectorEnabled: true},
		ChunkingConfig:   types.ChunkingConfig{ChunkSize: 100, ChunkOverlap: 10},
	}
	chunks := repository.NewChunkRepository(db)
	engine := &wikiIndexEngine{items: make(map[string]*types.IndexInfo)}
	embedder := &wikiIndexEmbedder{}
	svc := NewIndexedWikiPageService(repository.NewWikiPageRepository(db), chunks,
		&wikiIndexKB{kb: kb}, nil, nil, nil, &wikiIndexModel{embedder: embedder},
		wikiIndexRegistry{engine: engine}, nil, wikiIndexTenant{}).(*wikiPageService)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	return ctx, svc, chunks, engine, embedder
}

func wikiIndexPage() *types.WikiPage {
	return &types.WikiPage{
		ID: uuid.NewString(), TenantID: 7, KnowledgeBaseID: "kb-wiki", Slug: "concept/synthesis",
		Title: "Synthesized concept", Content: strings.Repeat("Wiki-only specialized knowledge.\n\n", 12),
		PageType: types.WikiPageTypeConcept, Status: types.WikiPageStatusPublished,
	}
}

func TestWikiPageIndexLifecycleAndSearch(t *testing.T) {
	ctx, svc, chunks, engine, embedder := newWikiIndexFixture(t)
	page, err := svc.CreatePage(ctx, wikiIndexPage())
	require.NoError(t, err)
	rows, err := chunks.ListAllChunksByKnowledgeID(ctx, 7, wikiPageKnowledgeID(page.ID))
	require.NoError(t, err)
	require.Greater(t, len(rows), 1, "long wiki pages must be split for embedding")
	require.Len(t, engine.items, len(rows))
	require.NotEmpty(t, embedder.texts, "wiki content must reach the embedding model")
	var hits []*types.IndexWithScore
	for _, row := range rows {
		require.Len(t, row.ID, 36)
		require.Equal(t, types.ChunkTypeWikiPage, row.ChunkType)
		require.Equal(t, "ready", row.IndexStatus)
		require.Equal(t, int(types.ChunkStatusIndexed), row.Status)
		require.Contains(t, engine.items[row.ID].Content, page.Title)
		hits = append(hits, &types.IndexWithScore{
			ChunkID: row.ID, KnowledgeID: wikiPageKnowledgeID(page.ID), Score: 0.8,
		})
	}
	search := &knowledgeBaseService{kgRepo: wikiIndexKnowledgeRepo{}, chunkRepo: chunks}
	results, err := search.processSearchResults(ctx, hits, true)
	require.NoError(t, err)
	require.Len(t, results, len(rows), "wiki hits must survive hydration without a document row")
	require.Equal(t, page.Title, results[0].KnowledgeTitle)
	require.Equal(t, page.KnowledgeBaseID, results[0].KnowledgeBaseID)
	require.Equal(t, types.ChunkTypeWikiPage, results[0].ChunkType)

	page.Content = "replacement unique wiki body"
	page.Title = "Renamed concept"
	page, err = svc.UpdatePage(ctx, page)
	require.NoError(t, err)
	rows, err = chunks.ListAllChunksByKnowledgeID(ctx, 7, wikiPageKnowledgeID(page.ID))
	require.NoError(t, err)
	require.Len(t, rows, 1, "shortening a page must remove surplus chunks and vectors")
	require.Len(t, engine.items, 1)
	require.Equal(t, page.Content, rows[0].Content)
	require.Contains(t, engine.items[rows[0].ID].Content, page.Title)

	page.Status = types.WikiPageStatusArchived
	require.NoError(t, svc.UpdatePageMeta(ctx, page))
	rows, err = chunks.ListAllChunksByKnowledgeID(ctx, 7, wikiPageKnowledgeID(page.ID))
	require.NoError(t, err)
	require.Empty(t, rows)
	require.Empty(t, engine.items)
	page.Status = types.WikiPageStatusPublished
	require.NoError(t, svc.UpdatePageMeta(ctx, page))
	require.NotEmpty(t, engine.items)
	require.NoError(t, svc.DeletePage(ctx, page.KnowledgeBaseID, page.Slug))
	rows, err = chunks.ListAllChunksByKnowledgeID(ctx, 7, wikiPageKnowledgeID(page.ID))
	require.NoError(t, err)
	require.Empty(t, rows)
	require.Empty(t, engine.items)
}

func TestWikiPageIndexPublicationAndRetry(t *testing.T) {
	ctx, svc, chunks, engine, embedder := newWikiIndexFixture(t)
	page := wikiIndexPage()
	page.Status = types.WikiPageStatusDraft
	_, err := svc.CreatePage(ctx, page)
	require.NoError(t, err)
	require.Empty(t, embedder.texts)
	page.Status = types.WikiPageStatusPublished
	engine.err = errors.New("embedding temporarily unavailable")
	require.ErrorContains(t, svc.UpdatePageMeta(ctx, page), "temporarily unavailable")
	rows, err := chunks.ListAllChunksByKnowledgeID(ctx, 7, wikiPageKnowledgeID(page.ID))
	require.NoError(t, err)
	require.NotEmpty(t, rows)
	for _, row := range rows {
		require.False(t, (&knowledgeBaseService{}).isSearchableChunk(row), "partial indexing must stay hidden")
	}
	engine.err = nil
	_, err = svc.UpdatePage(ctx, page)
	require.NoError(t, err, "unchanged page writes must repair a failed sync")
	require.NotEmpty(t, engine.items)
}

func TestWikiPageIndexExcludesIndexAndWrongTenant(t *testing.T) {
	ctx, svc, _, engine, embedder := newWikiIndexFixture(t)
	page := wikiIndexPage()
	page.PageType = types.WikiPageTypeIndex
	_, err := svc.CreatePage(ctx, page)
	require.NoError(t, err)
	require.Empty(t, embedder.texts)
	require.Empty(t, engine.items)
	page.PageType = types.WikiPageTypeConcept
	page.TenantID = 99
	require.ErrorContains(t, svc.syncPageIndex(ctx, page), "tenant mismatch")
	require.Empty(t, engine.items)
}

func TestWikiPageIndexFailureHidesStaleHitsAndDeletionRetries(t *testing.T) {
	ctx, svc, chunks, engine, _ := newWikiIndexFixture(t)
	page, err := svc.CreatePage(ctx, wikiIndexPage())
	require.NoError(t, err)
	model := svc.pageIndex.models.(*wikiIndexModel)
	model.err = errors.New("model unavailable")
	page.Content = "new content while model is unavailable"
	_, err = svc.UpdatePage(ctx, page)
	require.ErrorContains(t, err, "model unavailable")
	rows, err := chunks.ListAllChunksByKnowledgeID(ctx, 7, wikiPageKnowledgeID(page.ID))
	require.NoError(t, err)
	require.Empty(t, rows, "failed replacement must not expose the old page body")
	require.Empty(t, engine.items)
	model.err = nil
	_, err = svc.UpdatePage(ctx, page)
	require.NoError(t, err)
	engine.deleteErr = errors.New("store unavailable")
	require.ErrorContains(t, svc.DeletePage(ctx, page.KnowledgeBaseID, page.Slug), "store unavailable")
	_, err = svc.GetPageBySlug(ctx, page.KnowledgeBaseID, page.Slug)
	require.NoError(t, err, "failed index deletion must leave the page available for retry")
	engine.deleteErr = nil
	require.NoError(t, svc.DeletePage(ctx, page.KnowledgeBaseID, page.Slug))
	require.Empty(t, engine.items)
}

func TestWikiPageIndexNeverOverwritesSourceDocument(t *testing.T) {
	ctx, svc, chunks, _, _ := newWikiIndexFixture(t)
	page := wikiIndexPage()
	source := &types.Chunk{
		ID: uuid.NewString(), KnowledgeID: page.ID, TenantID: 7, KnowledgeBaseID: page.KnowledgeBaseID,
		Content: "original source document", ChunkType: types.ChunkTypeText, IsEnabled: true,
	}
	require.NoError(t, chunks.CreateChunks(ctx, []*types.Chunk{source}))
	// A caller's workspace must never select another workspace's models or engines.
	ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(99))
	_, err := svc.CreatePage(ctx, page)
	require.NoError(t, err)
	require.Equal(t, uint64(7), svc.pageIndex.models.(*wikiIndexModel).tenantID)
	require.NoError(t, svc.DeletePage(ctx, page.KnowledgeBaseID, page.Slug))
	stored, err := chunks.GetChunkByID(ctx, 7, source.ID)
	require.NoError(t, err)
	require.Equal(t, source.Content, stored.Content)
}

func (r wikiIndexRegistry) GetOrLoadByStoreID(
	context.Context, uint64, string,
) (interfaces.RetrieveEngineService, error) {
	return r.engine, nil
}

type wikiIndexOwnership struct {
	allowed  bool
	tenantID uint64
}

func (o *wikiIndexOwnership) StoreOwnedBy(_ context.Context, _ string, tenantID uint64) (bool, error) {
	o.tenantID = tenantID
	return o.allowed, nil
}

func TestWikiPageIndexChecksBoundStoreOwnership(t *testing.T) {
	ctx, svc, _, engine, _ := newWikiIndexFixture(t)
	storeID := "wiki-store"
	svc.pageIndex.kbs.(*wikiIndexKB).kb.VectorStoreID = &storeID
	owner := &wikiIndexOwnership{allowed: false}
	svc.pageIndex.ownership = owner
	svc.pageIndex.tenants = nil // A bound store must never use tenant-default engines.
	page := wikiIndexPage()
	require.Error(t, svc.syncPageIndex(ctx, page))
	require.Empty(t, engine.items)
	owner.allowed = true
	require.NoError(t, svc.syncPageIndex(ctx, page))
	require.Equal(t, page.TenantID, owner.tenantID)
	require.NotEmpty(t, engine.items)
}

func TestWikiPageIndexKnowledgeBaseCleanupRetriesBeforeRemovingPages(t *testing.T) {
	ctx, svc, chunks, engine, _ := newWikiIndexFixture(t)
	page, err := svc.CreatePage(ctx, wikiIndexPage())
	require.NoError(t, err)
	// Simulate a failed archive synchronization: archived rows must also be cleaned.
	page.Status = types.WikiPageStatusArchived
	require.NoError(t, svc.repo.UpdateMeta(ctx, page))
	cleanup := &knowledgeBaseService{
		wikiRepo: svc.repo, chunkRepo: chunks, retrieveEngine: wikiIndexRegistry{engine: engine},
	}
	payload := types.KBDeletePayload{
		TenantID: 7, KnowledgeBaseID: page.KnowledgeBaseID,
		EffectiveEngines: []types.RetrieverEngineParams{{
			RetrieverType: types.VectorRetrieverType, RetrieverEngineType: types.PostgresRetrieverEngineType,
		}},
	}
	engine.deleteErr = errors.New("store unavailable")
	require.ErrorContains(t, cleanup.cleanupWikiPageIndexes(ctx, payload), "store unavailable")
	_, err = svc.repo.GetBySlug(ctx, page.KnowledgeBaseID, page.Slug)
	require.NoError(t, err, "wiki rows must remain so a later cleanup attempt can find the indexes")
	engine.deleteErr = nil
	require.NoError(t, cleanup.cleanupWikiPageIndexes(ctx, payload))
	require.Empty(t, engine.items)
	rows, err := chunks.ListAllChunksByKnowledgeID(ctx, 7, wikiPageKnowledgeID(page.ID))
	require.NoError(t, err)
	require.Empty(t, rows)
}

func TestWikiPageIndexSearchHydratesWithoutDocument(t *testing.T) {
	ctx, _, chunks, _, _ := newWikiIndexFixture(t)
	page := wikiIndexPage()
	row := &types.Chunk{
		ID: uuid.NewString(), TenantID: 7, KnowledgeBaseID: page.KnowledgeBaseID,
		KnowledgeID: wikiPageKnowledgeID(page.ID), Content: "wiki-only term", ContextHeader: page.Title,
		ChunkType: types.ChunkTypeWikiPage, IsEnabled: true, IndexStatus: "ready",
	}
	require.NoError(t, chunks.CreateChunks(ctx, []*types.Chunk{row}))
	search := &knowledgeBaseService{kgRepo: wikiIndexKnowledgeRepo{}, chunkRepo: chunks}
	results, err := search.processSearchResults(ctx, []*types.IndexWithScore{{
		ChunkID: row.ID, KnowledgeID: row.KnowledgeID, Score: 0.8,
	}}, true)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, page.Title, results[0].KnowledgeTitle)
}

func TestWikiPageIndexDeletionDoesNotRequireEmbeddingModel(t *testing.T) {
	ctx, svc, chunks, engine, _ := newWikiIndexFixture(t)
	page, err := svc.CreatePage(ctx, wikiIndexPage())
	require.NoError(t, err)
	svc.pageIndex.models.(*wikiIndexModel).err = errors.New("model was removed")
	require.NoError(t, svc.DeletePage(ctx, page.KnowledgeBaseID, page.Slug))
	require.Empty(t, engine.items)
	rows, err := chunks.ListAllChunksByKnowledgeID(ctx, 7, wikiPageKnowledgeID(page.ID))
	require.NoError(t, err)
	require.Empty(t, rows)
}

func TestWikiPageIndexSearchPreservesTenantPermissions(t *testing.T) {
	ctx, svc, chunks, engine, _ := newWikiIndexFixture(t)
	page, err := svc.CreatePage(ctx, wikiIndexPage())
	require.NoError(t, err)
	var hits []*types.IndexWithScore
	for id := range engine.items {
		hits = append(hits, &types.IndexWithScore{
			ChunkID: id, KnowledgeID: wikiPageKnowledgeID(page.ID), Score: 0.8,
		})
	}
	ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(99))
	search := &knowledgeBaseService{kgRepo: wikiIndexKnowledgeRepo{}, chunkRepo: chunks}
	results, err := search.processSearchResults(ctx, hits, true)
	require.NoError(t, err)
	require.Empty(t, results, "wiki hydration must not bypass KB read permissions")
}
