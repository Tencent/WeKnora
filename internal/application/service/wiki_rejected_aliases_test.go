package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func createAliasPage(t *testing.T, ctx context.Context, h wikiRevisionTestHarness, aliases ...string) *types.WikiPage {
	t.Helper()
	page, err := h.svc.CreatePage(ctx, &types.WikiPage{
		KnowledgeBaseID: "kb-alias", TenantID: 1, Slug: "concept/buddha-nature", Title: "佛性",
		PageType: types.WikiPageTypeConcept, Content: "body", Aliases: aliases,
	})
	require.NoError(t, err)
	return page
}

func writeAliases(
	t *testing.T, ctx context.Context, h wikiRevisionTestHarness, source string, page *types.WikiPage, aliases ...string,
) *types.WikiPage {
	t.Helper()
	edit := *page
	edit.Aliases = aliases
	updated, err := h.svc.UpdatePage(types.WithWikiEditSource(ctx, source), &edit)
	require.NoError(t, err)
	return updated
}

func TestUserRemovedAliasCannotComeBackThroughAutomatedWrites(t *testing.T) {
	for _, source := range []string{types.WikiEditSourcePipeline, types.WikiEditSourceAgent} {
		t.Run(source, func(t *testing.T) {
			ctx, h, _ := newWikiRevisionTestService(t)
			page := createAliasPage(t, ctx, h, "如来藏", "佛种")

			page = writeAliases(t, ctx, h, types.WikiEditSourceUser, page, "佛种")
			require.Equal(t, []string{"如来藏"}, page.RejectedAliases())
			version := page.Version

			page = writeAliases(t, ctx, h, source, page, "佛种", "如来藏", "佛性论")
			require.Equal(t, types.StringArray{"佛种", "佛性论"}, page.Aliases)
			require.Equal(t, []string{"如来藏"}, page.RejectedAliases())
			require.Equal(t, version+1, page.Version)
		})
	}
}

func TestRejectedAliasMatchIgnoresCaseAndSpacing(t *testing.T) {
	ctx, h, _ := newWikiRevisionTestService(t)
	page := createAliasPage(t, ctx, h, "RAG")

	page = writeAliases(t, ctx, h, types.WikiEditSourceUser, page)
	page = writeAliases(t, ctx, h, types.WikiEditSourcePipeline, page, " rag ")

	require.Empty(t, page.Aliases)
}

func TestAutomatedWriteThatOnlyReAddsRejectedAliasDoesNotBumpVersion(t *testing.T) {
	ctx, h, _ := newWikiRevisionTestService(t)
	page := createAliasPage(t, ctx, h, "如来藏")
	page = writeAliases(t, ctx, h, types.WikiEditSourceUser, page)
	version := page.Version

	page = writeAliases(t, ctx, h, types.WikiEditSourcePipeline, page, "如来藏")

	require.Equal(t, version, page.Version)
	require.Empty(t, page.Aliases)
}

func TestUserCanRestoreRejectedAlias(t *testing.T) {
	ctx, h, _ := newWikiRevisionTestService(t)
	page := createAliasPage(t, ctx, h, "如来藏")
	page = writeAliases(t, ctx, h, types.WikiEditSourceUser, page)

	page = writeAliases(t, ctx, h, types.WikiEditSourceUser, page, "如来藏")
	require.Equal(t, types.StringArray{"如来藏"}, page.Aliases)
	require.Empty(t, page.RejectedAliases())

	page = writeAliases(t, ctx, h, types.WikiEditSourcePipeline, page, "如来藏", "佛种")
	require.Equal(t, types.StringArray{"如来藏", "佛种"}, page.Aliases)
}

func TestRevertRestoringAliasLiftsRejection(t *testing.T) {
	ctx, h, _ := newWikiRevisionTestService(t)
	page := createAliasPage(t, ctx, h, "如来藏")
	writeAliases(t, ctx, h, types.WikiEditSourceUser, page)

	reverted, err := h.svc.RevertPageToVersion(ctx, "kb-alias", "concept/buddha-nature", page.Version)
	require.NoError(t, err)

	require.Equal(t, types.StringArray{"如来藏"}, reverted.Aliases)
	require.Empty(t, reverted.RejectedAliases())
}

func TestRejectedAliasesKeepOtherPageMetadata(t *testing.T) {
	ctx, h, _ := newWikiRevisionTestService(t)
	page := createAliasPage(t, ctx, h, "如来藏")
	edit := *page
	edit.PageMetadata = types.JSON(`{"note":"keep","rank":12345678901234567}`)
	page, err := h.svc.UpdatePage(ctx, &edit)
	require.NoError(t, err)

	page = writeAliases(t, ctx, h, types.WikiEditSourceUser, page)

	require.JSONEq(t, `{"note":"keep","rank":12345678901234567,"rejected_aliases":["如来藏"]}`, string(page.PageMetadata))
}

func TestPagesWithoutRejectionsKeepMetadataUntouched(t *testing.T) {
	ctx, h, _ := newWikiRevisionTestService(t)
	page := createAliasPage(t, ctx, h, "佛种")
	before := string(page.PageMetadata)

	page = writeAliases(t, ctx, h, types.WikiEditSourcePipeline, page, "佛种", "佛性论")

	require.Equal(t, before, string(page.PageMetadata))
}

func TestReduceSkipsRejectedAliasesAndOtherPagesTitles(t *testing.T) {
	const stored = "PMP body"
	pageSvc, page := newWikiWriteGuardStore(t, stored)
	ctx := context.Background()
	_, err := pageSvc.CreatePage(ctx, &types.WikiPage{
		TenantID: 1, KnowledgeBaseID: "kb-row-guard", Slug: "concept/project-management",
		Title: "项目管理", PageType: types.WikiPageTypeConcept, Content: "other",
	})
	require.NoError(t, err)

	edit := *page
	edit.Aliases = types.StringArray{"证书"}
	userCtx := types.WithWikiEditSource(ctx, types.WikiEditSourceUser)
	page, err = pageSvc.UpdatePage(userCtx, &edit)
	require.NoError(t, err)
	edit = *page
	edit.Aliases = types.StringArray{}
	_, err = pageSvc.UpdatePage(userCtx, &edit)
	require.NoError(t, err)

	svc := &wikiIngestService{wikiService: pageSvc, knowledgeSvc: liveKnowledgeStub{}}
	batchCtx := &WikiBatchContext{
		SlugTitleMany: func(context.Context, []string) map[string]string { return nil },
	}
	updates := []SlugUpdate{{
		Slug: "entity/pmp", Type: types.WikiPageTypeEntity, KnowledgeID: "k-row-guard",
		DocTitle: "doc", SourceRef: "k-row-guard|doc",
		Item: extractedItem{
			Name: "PMP", Description: "d",
			Aliases: []string{"证书", "项目管理", "Project Management Professional"},
		},
	}}
	_, _, _, _, err = svc.reduceSlugUpdates(ctx, &templateCaptureChatModel{response: stored},
		"kb-row-guard", "entity/pmp", updates, 1, batchCtx, nil)
	require.NoError(t, err)

	after, err := pageSvc.GetPageBySlug(ctx, "kb-row-guard", "entity/pmp")
	require.NoError(t, err)
	require.Equal(t, types.StringArray{"Project Management Professional"}, after.Aliases)
}
