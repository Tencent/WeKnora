package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type relativeLinkKBService struct {
	interfaces.KnowledgeBaseService
	kb *types.KnowledgeBase
}

func setupRelativeLinkService(t *testing.T, kb *types.KnowledgeBase) (
	context.Context, interfaces.WikiPageService, interfaces.WikiPageRepository,
) {
	t.Helper()
	_, _, db := newWikiRevisionTestService(t)
	require.NoError(t, db.AutoMigrate(&types.WikiPageIssue{}))
	repo := repository.NewWikiPageRepository(db)
	svc := NewWikiPageService(repo, nil, &wikiSearchKBService{
		byID: map[string]*types.KnowledgeBase{kb.ID: kb},
	}, nil, nil, nil)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	return ctx, svc, repo
}

func (s *relativeLinkKBService) GetKnowledgeBaseByIDOnly(context.Context, string) (*types.KnowledgeBase, error) {
	return s.kb, nil
}

func TestWikiRelativeLinksRepairPreservesHistory(t *testing.T) {
	kb := makeWikiSearchKB("kb-repair", 1)
	ctx, svc, repo := setupRelativeLinkService(t, kb)
	body := strings.Repeat("Useful source material. ", 6) + "[gateway](../gateway.md)"
	page := &types.WikiPage{
		ID: "old-page", TenantID: 1, KnowledgeBaseID: kb.ID, Slug: "concept/gateway",
		Title: "Gateway", PageType: types.WikiPageTypeConcept, Status: types.WikiPageStatusPublished,
		Content: body, Summary: "[gateway](gateway.md)", Version: 1,
	}
	// Bypass the write service to simulate rows stored before this fix.
	require.NoError(t, repo.Create(ctx, page))
	lint := NewWikiLintService(svc, &relativeLinkKBService{kb: kb}, nil)
	report, err := lint.RunLint(ctx, kb.ID)
	require.NoError(t, err)
	var relativeIssues int
	for _, issue := range report.Issues {
		if issue.Type == LintIssueRelativeMarkdownLink {
			relativeIssues++
		}
	}
	require.Equal(t, 1, relativeIssues)
	fixed, err := lint.AutoFix(ctx, kb.ID)
	require.NoError(t, err)
	require.Equal(t, 1, fixed)
	stored, err := repo.GetBySlug(ctx, kb.ID, page.Slug)
	require.NoError(t, err)
	require.Equal(t, strings.Repeat("Useful source material. ", 6)+"gateway", stored.Content)
	require.Equal(t, "gateway", stored.Summary)
	require.Equal(t, 2, stored.Version)
	revision, err := svc.GetRevision(ctx, kb.ID, page.Slug, 1)
	require.NoError(t, err)
	require.Equal(t, body, revision.Content)
	hits, err := svc.SearchPagesAcross(ctx, []string{kb.ID}, "gateway", 10)
	require.NoError(t, err)
	require.Len(t, hits, 1)
	require.Equal(t, stored.Content, hits[0].Content)
	fixed, err = lint.AutoFix(ctx, kb.ID)
	require.NoError(t, err)
	require.Zero(t, fixed)
	stored, err = repo.GetBySlug(ctx, kb.ID, page.Slug)
	require.NoError(t, err)
	require.Equal(t, 2, stored.Version)
}

type conflictingRelativeLinkService struct {
	interfaces.WikiPageService
	t *testing.T
}

func (s *conflictingRelativeLinkService) UpdatePage(
	ctx context.Context, page *types.WikiPage,
) (*types.WikiPage, error) {
	snapshot, ok := types.WikiPageWriteSnapshotFromContext(ctx)
	require.True(s.t, ok)
	require.Contains(s.t, snapshot.Content, "../gateway.md")
	require.NotContains(s.t, page.Content, "../gateway.md")
	return nil, repository.ErrWikiPageConflict
}

func TestWikiRelativeLinksRepairReportsConflict(t *testing.T) {
	kb := makeWikiSearchKB("kb-conflict", 1)
	ctx, svc, repo := setupRelativeLinkService(t, kb)
	body := strings.Repeat("Keep this source material. ", 6) + "[gateway](../gateway.md)"
	require.NoError(t, repo.Create(ctx, &types.WikiPage{
		ID: "old-page", TenantID: 1, KnowledgeBaseID: kb.ID, Slug: "concept/gateway",
		Title: "Gateway", PageType: types.WikiPageTypeConcept, Status: types.WikiPageStatusPublished,
		Content: body, Version: 1,
	}))
	lint := NewWikiLintService(&conflictingRelativeLinkService{WikiPageService: svc, t: t},
		&relativeLinkKBService{kb: kb}, nil)
	fixed, err := lint.AutoFix(ctx, kb.ID)
	require.True(t, errors.Is(err, repository.ErrWikiPageConflict))
	require.Zero(t, fixed)
	stored, err := repo.GetBySlug(ctx, kb.ID, "concept/gateway")
	require.NoError(t, err)
	require.Equal(t, body, stored.Content)
	require.Equal(t, 1, stored.Version)
}

func TestWikiRelativeLinksPersistAndSearch(t *testing.T) {
	ctx, svc, repo := setupWikiSearchService(t, map[string]*types.KnowledgeBase{
		"kb-links": makeWikiSearchKB("kb-links", 1),
	}, nil)
	page, err := svc.CreatePage(ctx, &types.WikiPage{
		TenantID: 1, KnowledgeBaseID: "kb-links", Slug: "concept/gateway", Title: "Gateway",
		PageType: types.WikiPageTypeConcept,
		Content:  "Read [query gateway](rdm/query-gateway.md) and [飞书](../lark.md).",
		Summary:  "See [gateway](./gateway.md).",
	})
	require.NoError(t, err)
	stored, err := repo.GetBySlug(ctx, "kb-links", page.Slug)
	require.NoError(t, err)
	require.Equal(t, "Read query gateway and 飞书.", stored.Content)
	require.Equal(t, "See gateway.", stored.Summary)
	got, err := svc.GetPageBySlug(ctx, "kb-links", page.Slug)
	require.NoError(t, err)
	require.Equal(t, stored.Content, got.Content)
	hits, err := svc.SearchPagesAcross(ctx, []string{"kb-links"}, "gateway", 10)
	require.NoError(t, err)
	require.Len(t, hits, 1)
	require.Equal(t, stored.Content, hits[0].Content)

	page.Content = "Updated [gateway](../gateway.md#details)."
	updated, err := svc.UpdatePage(ctx, page)
	require.NoError(t, err)
	require.Equal(t, "Updated gateway.", updated.Content)
	revision, err := svc.GetRevision(ctx, "kb-links", page.Slug, 1)
	require.NoError(t, err)
	require.Equal(t, stored.Content, revision.Content)

	updated.Content = "Auto-linked [gateway](../gateway.md) and [[concept/other]]."
	require.NoError(t, svc.UpdateAutoLinkedContent(ctx, updated))
	stored, err = repo.GetBySlug(ctx, "kb-links", page.Slug)
	require.NoError(t, err)
	require.Equal(t, "Auto-linked gateway and [[concept/other]].", stored.Content)
	require.Equal(t, types.StringArray{"concept/other"}, stored.OutLinks)
}
