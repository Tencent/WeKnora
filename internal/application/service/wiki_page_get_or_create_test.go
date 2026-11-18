package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// TestGetOrCreatePageReturnsWinnerOnSlugRace covers the ingest side of
// Tencent/WeKnora#5656: when a concurrent batch created the slug after
// the exists snapshot, the losing batch gets the winner's row back
// instead of a unique-key failure, and can then apply its content
// through the normal versioned update path.
func TestGetOrCreatePageReturnsWinnerOnSlugRace(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.KnowledgeBase{}, &types.WikiFolder{}, &types.WikiPage{}, &types.WikiPageRevision{}))
	// Align with the production migration: the slug is unique per
	// knowledge base, not globally. The model tag's single-column index
	// would otherwise reject the same slug in another KB and shadow the
	// partial index under test.
	require.NoError(t, db.Exec("DROP INDEX IF EXISTS idx_kb_slug").Error)
	require.NoError(t, db.Exec(`
		CREATE UNIQUE INDEX idx_wiki_pages_kb_slug
		ON wiki_pages (knowledge_base_id, slug)
		WHERE deleted_at IS NULL
	`).Error)

	ctx := context.Background()
	repo := repository.NewWikiPageRepository(db)
	svc := NewWikiPageService(repo, nil, nil, nil, nil, nil)
	seedWikiTestKnowledgeBase(t, db, "kb-race", 1)

	winner, created, err := svc.GetOrCreatePage(ctx, &types.WikiPage{
		TenantID: 1, KnowledgeBaseID: "kb-race", Slug: "entity/acme",
		Title: "Acme", Content: "winner body",
		PageType: types.WikiPageTypeEntity,
	})
	require.NoError(t, err)
	require.True(t, created)
	require.NotEmpty(t, winner.ID)

	// The losing batch passes its own synthesized page: same slug, new
	// content, fresh id. It must get the winner back, untouched.
	loser := &types.WikiPage{
		TenantID: 1, KnowledgeBaseID: "kb-race", Slug: "entity/acme",
		Title: "Acme", Content: "loser body",
		PageType: types.WikiPageTypeEntity,
	}
	existing, created, err := svc.GetOrCreatePage(ctx, loser)
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, winner.ID, existing.ID)
	require.Equal(t, "winner body", existing.Content)

	// The ingest fallback adopts the winner's identity and updates it.
	loser.ID = existing.ID
	loser.Version = existing.Version
	updated, err := svc.UpdatePage(ctx, loser)
	require.NoError(t, err)
	require.Equal(t, winner.ID, updated.ID)
	require.Equal(t, "loser body", updated.Content)
	require.Equal(t, 2, updated.Version)

	stored, err := svc.GetPageBySlug(ctx, "kb-race", "entity/acme")
	require.NoError(t, err)
	require.Equal(t, "loser body", stored.Content)
}
