package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type wikiRenameChunkRepo struct {
	interfaces.ChunkRepository
	chunks            map[string]*types.Chunk
	getErr, updateErr error
	gets, updates     int
	onGet             func()
}

func (r *wikiRenameChunkRepo) GetChunkByID(_ context.Context, _ uint64, id string) (*types.Chunk, error) {
	r.gets++
	if r.onGet != nil {
		r.onGet()
	}
	if r.getErr != nil {
		return nil, r.getErr
	}
	if chunk := r.chunks[id]; chunk != nil {
		cloned := *chunk
		return &cloned, nil
	}
	return nil, repository.ErrChunkNotFound
}

func (r *wikiRenameChunkRepo) UpdateRenamedWikiChunk(
	_ context.Context,
	_ *types.WikiPage,
	chunk *types.Chunk,
	expectedUpdatedAt time.Time,
) error {
	r.updates++
	if r.updateErr != nil {
		return r.updateErr
	}
	if !r.chunks[chunk.ID].UpdatedAt.Equal(expectedUpdatedAt) {
		return repository.ErrChunkRevisionConflict
	}
	cloned := *chunk
	r.chunks[chunk.ID] = &cloned
	return nil
}

func newWikiRenameService(
	t *testing.T,
	chunks interfaces.ChunkRepository,
) (*gorm.DB, interfaces.WikiPageRepository, interfaces.WikiPageService, *types.WikiPage) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	pool.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	require.NoError(
		t,
		db.AutoMigrate(&types.WikiPage{}, &types.WikiPageRevision{}, &types.WikiPageIssue{}, &types.WikiFolder{}),
	)
	require.NoError(t, db.Migrator().DropIndex(&types.WikiPage{}, "idx_kb_slug"))
	require.NoError(t, db.Exec(`CREATE UNIQUE INDEX idx_wiki_pages_kb_slug
		ON wiki_pages (knowledge_base_id, slug) WHERE deleted_at IS NULL`).Error)
	repo := repository.NewWikiPageRepository(db)
	svc := NewWikiPageService(repo, chunks, nil, nil, nil)
	page, err := svc.CreatePage(context.Background(), &types.WikiPage{
		TenantID:        42,
		KnowledgeBaseID: "kb-a",
		Slug:            "concept/old",
		Title:           "Topic",
		Content:         "original content",
		PageType:        types.WikiPageTypeConcept,
		SourceRefs:      types.StringArray{"doc-id|Source"},
		ChunkRefs:       types.StringArray{"source-chunk"},
	})
	require.NoError(t, err)
	return db, repo, svc, page
}

func renameServiceRequest(page *types.WikiPage) interfaces.WikiPageRenameRequest {
	return interfaces.WikiPageRenameRequest{
		KnowledgeBaseID: page.KnowledgeBaseID,
		PageID:          page.ID,
		OldSlug:         page.Slug,
		NewSlug:         "concept/new",
	}
}

func TestWikiRenameServiceKeepsHistoryAccessible(t *testing.T) {
	_, _, svc, page := newWikiRenameService(t, nil)
	ctx := context.Background()
	page.Content = "edited content"
	page, err := svc.UpdatePage(ctx, page)
	require.NoError(t, err)
	createdAt := page.CreatedAt
	result, err := svc.(interfaces.WikiPageRenamer).RenamePage(ctx, renameServiceRequest(page))
	require.NoError(t, err)
	require.Equal(t, page.ID, result.Page.ID)
	require.Equal(t, 2, result.Page.Version)
	require.True(t, createdAt.Equal(result.Page.CreatedAt))
	require.Equal(t, page.SourceRefs, result.Page.SourceRefs)
	require.Equal(t, page.ChunkRefs, result.Page.ChunkRefs)
	history, err := svc.ListRevisions(ctx, page.KnowledgeBaseID, "concept/new", 10, 0)
	require.NoError(t, err)
	require.EqualValues(t, 1, history.Total)
	require.Equal(t, 2, history.CurrentVersion)
	revision, err := svc.GetRevision(ctx, page.KnowledgeBaseID, "concept/new", 1)
	require.NoError(t, err)
	require.Equal(t, page.ID, revision.PageID)
	require.Equal(t, "original content", revision.Content)
	require.Equal(t, "concept/old", revision.Slug, "historical snapshots retain their original slug")
	_, err = svc.GetPageBySlug(ctx, page.KnowledgeBaseID, "concept/old")
	require.ErrorIs(t, err, repository.ErrWikiPageNotFound)
}

func TestWikiRenameServiceSyncsOnlyExistingWikiChunksAfterCommit(t *testing.T) {
	chunks := &wikiRenameChunkRepo{chunks: make(map[string]*types.Chunk)}
	_, repo, svc, page := newWikiRenameService(t, chunks)
	chunkID := "wp-" + page.ID
	createdAt := time.Now().Add(-time.Hour)
	chunks.chunks[chunkID] = &types.Chunk{
		ID: chunkID, TenantID: page.TenantID, KnowledgeBaseID: page.KnowledgeBaseID, KnowledgeID: page.ID,
		ChunkType: types.ChunkTypeWikiPage, Content: "# Topic\n\noriginal content", SourceContent: "immutable source",
		ContentRevision: 3, IndexStatus: "ready", CreatedAt: createdAt, IsEnabled: false,
		Metadata: types.JSON(`{"wiki_slug":"concept/old","slug":"concept/old","custom":{"precise":9007199254740993}}`),
	}
	chunks.onGet = func() {
		_, err := repo.GetBySlug(context.Background(), page.KnowledgeBaseID, "concept/old")
		require.ErrorIs(t, err, repository.ErrWikiPageNotFound)
	}
	result, err := svc.(interfaces.WikiPageRenamer).RenamePage(context.Background(), renameServiceRequest(page))
	require.NoError(t, err)
	require.Empty(t, result.SyncWarnings)
	require.Equal(t, 1, chunks.gets)
	require.Equal(t, 1, chunks.updates)
	chunk := chunks.chunks[chunkID]
	require.Equal(t, chunkID, chunk.ID)
	require.Equal(t, "# Topic\n\noriginal content", chunk.Content)
	require.Equal(t, "immutable source", chunk.SourceContent)
	require.Equal(t, 3, chunk.ContentRevision)
	require.Equal(t, createdAt, chunk.CreatedAt)
	require.False(t, chunk.IsEnabled)
	require.Equal(t, "ready", chunk.IndexStatus)
	var metadata map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(chunk.Metadata, &metadata))
	require.JSONEq(t, `"concept/new"`, string(metadata["wiki_slug"]))
	require.JSONEq(t, `"concept/new"`, string(metadata["slug"]))
	require.JSONEq(t, `{"precise":9007199254740993}`, string(metadata["custom"]))
	var pageID string
	require.NoError(t, json.Unmarshal(metadata["wiki_page_id"], &pageID))
	require.Equal(t, page.ID, pageID)
}

func TestWikiRenameServiceSyncsRewrittenBodiesAndExposesReindexLimit(t *testing.T) {
	chunks := &wikiRenameChunkRepo{chunks: make(map[string]*types.Chunk)}
	_, repo, svc, page := newWikiRenameService(t, chunks)
	ctx := context.Background()
	page.Content = "self [[concept/old]]"
	require.NoError(t, svc.UpdateAutoLinkedContent(ctx, page))
	other, err := svc.CreatePage(ctx, &types.WikiPage{
		TenantID: page.TenantID, KnowledgeBaseID: page.KnowledgeBaseID,
		Slug: "concept/from", Title: "From", Content: "see [[concept/old]]", PageType: types.WikiPageTypeConcept,
	})
	require.NoError(t, err)
	for _, p := range []*types.WikiPage{page, other} {
		chunks.chunks["wp-"+p.ID] = &types.Chunk{
			ID: "wp-" + p.ID, TenantID: p.TenantID, KnowledgeBaseID: p.KnowledgeBaseID,
			ChunkType: types.ChunkTypeWikiPage, Content: p.Content, IsEnabled: false, IndexStatus: "ready",
		}
	}
	result, err := svc.(interfaces.WikiPageRenamer).RenamePage(ctx, renameServiceRequest(page))
	require.NoError(t, err)
	require.Len(t, result.SyncWarnings, 2)
	for _, p := range result.AffectedPages {
		chunk := chunks.chunks["wp-"+p.ID]
		stored, err := repo.GetByID(ctx, p.ID)
		require.NoError(t, err)
		require.Equal(t, stored.Content, chunk.Content)
		require.NotContains(t, chunk.Content, "[[concept/old]]")
		require.Equal(t, "failed", chunk.IndexStatus)
		require.False(t, chunk.IsEnabled)
		require.NotEmpty(t, chunk.ContentHash)
	}
}

func TestWikiRenameServiceSyncFailuresDoNotUndoRename(t *testing.T) {
	for _, scenario := range []string{
		"missing", "read-error", "write-error", "metadata-error", "wrong-kb", "source-chunk",
	} {
		t.Run(scenario, func(t *testing.T) {
			chunks := &wikiRenameChunkRepo{chunks: make(map[string]*types.Chunk)}
			_, repo, svc, page := newWikiRenameService(t, chunks)
			chunk := &types.Chunk{
				ID:              "wp-" + page.ID,
				TenantID:        page.TenantID,
				KnowledgeBaseID: page.KnowledgeBaseID,
				ChunkType:       types.ChunkTypeWikiPage,
				Content:         page.Content,
			}
			chunks.chunks[chunk.ID] = chunk
			switch scenario {
			case "missing":
				delete(chunks.chunks, chunk.ID)
			case "read-error":
				chunks.getErr = errors.New("lookup failed")
			case "write-error":
				chunks.updateErr = errors.New("write failed")
			case "metadata-error":
				chunk.Metadata = types.JSON(`not json`)
			case "wrong-kb":
				chunk.KnowledgeBaseID = "other-kb"
			case "source-chunk":
				chunk.ChunkType = types.ChunkTypeText
			}
			result, err := svc.(interfaces.WikiPageRenamer).RenamePage(context.Background(), renameServiceRequest(page))
			require.NoError(t, err)
			stored, err := repo.GetByID(context.Background(), page.ID)
			require.NoError(t, err)
			require.Equal(t, "concept/new", stored.Slug)
			if scenario == "missing" {
				require.Empty(t, result.SyncWarnings)
			} else {
				require.Len(t, result.SyncWarnings, 1)
			}
			if scenario != "write-error" {
				require.Zero(t, chunks.updates)
			}
		})
	}
}

func TestWikiRenameServiceRejectsUnsupportedRepositoryWithoutFallback(t *testing.T) {
	_, repo, _, page := newWikiRenameService(t, nil)
	legacyRepo := struct{ interfaces.WikiPageRepository }{repo}
	svc := NewWikiPageService(legacyRepo, nil, nil, nil, nil)
	result, err := svc.(interfaces.WikiPageRenamer).RenamePage(context.Background(), renameServiceRequest(page))
	require.ErrorIs(t, err, interfaces.ErrWikiRenameUnsupported)
	require.Nil(t, result)
	stored, err := repo.GetByID(context.Background(), page.ID)
	require.NoError(t, err)
	require.Equal(t, "concept/old", stored.Slug)
}

func TestWikiRenameServiceDoesNotSyncAfterCollision(t *testing.T) {
	chunks := &wikiRenameChunkRepo{}
	_, repo, svc, page := newWikiRenameService(t, chunks)
	_, err := svc.CreatePage(context.Background(), &types.WikiPage{
		TenantID:        page.TenantID,
		KnowledgeBaseID: page.KnowledgeBaseID,
		Slug:            "concept/new",
		Title:           "Existing",
		PageType:        types.WikiPageTypeConcept,
	})
	require.NoError(t, err)
	result, err := svc.(interfaces.WikiPageRenamer).RenamePage(context.Background(), renameServiceRequest(page))
	require.ErrorIs(t, err, repository.ErrWikiPageSlugConflict)
	require.Nil(t, result)
	require.Zero(t, chunks.gets)
	stored, err := repo.GetByID(context.Background(), page.ID)
	require.NoError(t, err)
	require.Equal(t, "concept/old", stored.Slug)
}

func TestWikiRenameServiceSyncRequiresConditionalUpdater(t *testing.T) {
	chunks := &wikiRenameChunkRepo{chunks: make(map[string]*types.Chunk)}
	_, repo, _, page := newWikiRenameService(t, nil)
	chunks.chunks["wp-"+page.ID] = &types.Chunk{
		ID: "wp-" + page.ID, TenantID: page.TenantID, KnowledgeBaseID: page.KnowledgeBaseID,
		ChunkType: types.ChunkTypeWikiPage, Content: page.Content,
	}
	legacy := struct{ interfaces.ChunkRepository }{chunks}
	svc := NewWikiPageService(repo, legacy, nil, nil, nil)
	result, err := svc.(interfaces.WikiPageRenamer).RenamePage(context.Background(), renameServiceRequest(page))
	require.NoError(t, err)
	require.Len(t, result.SyncWarnings, 1)
	require.Contains(t, result.SyncWarnings[0], "conditional wiki chunk sync is not supported")
	require.Zero(t, chunks.updates)
}
