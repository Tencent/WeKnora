package service

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type stubChunkRepoForSourceChunks struct {
	interfaces.ChunkRepository
	chunks map[string]*types.Chunk
	err    error
}

func (s *stubChunkRepoForSourceChunks) ListChunksByIDOnly(_ context.Context, ids []string) ([]*types.Chunk, error) {
	if s.err != nil {
		return nil, s.err
	}
	out := make([]*types.Chunk, 0, len(ids))
	for _, id := range ids {
		if c, ok := s.chunks[id]; ok {
			out = append(out, c)
		}
	}
	return out, nil
}

func TestListSourceChunksBySlugReturnsAllCitedChunks(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:wiki-source-chunks?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.WikiFolder{}, &types.WikiPage{}, &types.WikiPageRevision{}))

	ctx := context.Background()
	repo := repository.NewWikiPageRepository(db)
	now := time.Now()
	require.NoError(t, repo.Create(ctx, &types.WikiPage{
		ID: "p1", TenantID: 1, KnowledgeBaseID: "kb-1",
		Slug: "concept/root-crack", Title: "叶根断裂", PageType: types.WikiPageTypeConcept,
		Status: types.WikiPageStatusPublished, Content: "叶根断裂页",
		SourceRefs: types.StringArray{"doc-a|手册A", "doc-b|手册B"},
		ChunkRefs:  types.StringArray{"chunk-1", "chunk-2", "chunk-1", "", "chunk-missing"},
		Version:    1, CreatedAt: now, UpdatedAt: now,
	}))

	svc := NewWikiPageService(repo, &stubChunkRepoForSourceChunks{
		chunks: map[string]*types.Chunk{
			"chunk-1": {ID: "chunk-1", KnowledgeID: "doc-a", ChunkIndex: 3, Content: "叶根裂纹原文一段"},
			"chunk-2": {ID: "chunk-2", KnowledgeID: "doc-b", ChunkIndex: 1, Content: "疲劳扩展原文二段"},
		},
	}, nil, nil, nil, nil)

	got, err := svc.ListSourceChunksBySlug(ctx, "kb-1", "concept/root-crack")
	require.NoError(t, err)
	require.Equal(t, "concept/root-crack", got.Slug)
	require.Equal(t, "叶根断裂", got.Title)
	require.Equal(t, 3, got.ChunkRefCount)
	require.Equal(t, 1, got.MissingCount)
	require.Empty(t, got.Reason)
	require.Len(t, got.Chunks, 3)
	require.Equal(t, "chunk-1", got.Chunks[0].ID)
	require.Equal(t, "手册A", got.Chunks[0].KnowledgeTitle)
	require.Equal(t, "叶根裂纹原文一段", got.Chunks[0].Content)
	require.Equal(t, "chunk-2", got.Chunks[1].ID)
	require.Equal(t, "手册B", got.Chunks[1].KnowledgeTitle)
	require.True(t, got.Chunks[2].Missing)
	require.Equal(t, "chunk-missing", got.Chunks[2].ID)
}

func TestListSourceChunksBySlugEmptyRefs(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:wiki-source-chunks-empty?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.WikiFolder{}, &types.WikiPage{}, &types.WikiPageRevision{}))

	ctx := context.Background()
	repo := repository.NewWikiPageRepository(db)
	now := time.Now()
	require.NoError(t, repo.Create(ctx, &types.WikiPage{
		ID: "p-sum", TenantID: 1, KnowledgeBaseID: "kb-1",
		Slug: "summary/doc-a", Title: "手册A - Summary", PageType: types.WikiPageTypeSummary,
		Status: types.WikiPageStatusPublished, Content: "整篇摘要",
		SourceRefs: types.StringArray{"doc-a|手册A"},
		Version:    1, CreatedAt: now, UpdatedAt: now,
	}))

	svc := NewWikiPageService(repo, &stubChunkRepoForSourceChunks{}, nil, nil, nil, nil)
	got, err := svc.ListSourceChunksBySlug(ctx, "kb-1", "summary/doc-a")
	require.NoError(t, err)
	require.Equal(t, types.WikiSourceChunksReasonNoRefs, got.Reason)
	require.Empty(t, got.Chunks)
	require.Equal(t, 0, got.ChunkRefCount)
	require.Len(t, got.Sources, 1)
}

func TestListSourceChunksBySlugNotFound(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:wiki-source-chunks-missing?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.WikiFolder{}, &types.WikiPage{}, &types.WikiPageRevision{}))

	svc := NewWikiPageService(repository.NewWikiPageRepository(db), nil, nil, nil, nil, nil)
	_, err = svc.ListSourceChunksBySlug(context.Background(), "kb-1", "entity/missing")
	require.ErrorIs(t, err, repository.ErrWikiPageNotFound)
}
