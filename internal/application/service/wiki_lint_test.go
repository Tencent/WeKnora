package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestWikiAutoFixBrokenLinks(t *testing.T) {
	backlinkBackends(t, func(t *testing.T, h backlinkHarness) {
		ctx := context.Background()
		const legacySlug = "concept/bingfa-kuorong-tunTu"
		const brokenSlug = "concept/bingfa-kuorong-tuntu"
		const liveSlug = "concept/database"
		const unrelatedSlug = "entity/bingfa-kuorong-tuntu"
		code := "\n\n" + "```md\n[[" + brokenSlug + "]]\n```"
		body := "References in this wiki page: [[" + brokenSlug + "]] and [[" + brokenSlug +
			"|Concurrency]]. Also [[ Concept/Missing |Keep this label]] and [[" + liveSlug +
			"]]. The remaining prose is long enough to stay published." + code
		source := seedBacklinkPage(t, h, "summary/source", body, nil,
			[]string{brokenSlug, "concept/missing", liveSlug, "concept/stale-edge"})
		for _, slug := range []string{legacySlug, liveSlug, unrelatedSlug} {
			seedBacklinkPage(t, h, slug, "This target has enough content to stay published during lint.", nil, nil)
		}
		lint := NewWikiLintService(h.svc, &wikiLookupKBService{kb: &types.KnowledgeBase{
			IndexingStrategy: types.IndexingStrategy{WikiEnabled: true},
		}}, nil)

		fixed, err := lint.AutoFix(ctx, backlinkTestKB)
		require.NoError(t, err)
		require.Equal(t, 2, fixed)
		stored, err := h.svc.GetPageBySlug(ctx, backlinkTestKB, source.Slug)
		require.NoError(t, err)
		require.Equal(t, "References in this wiki page: [["+legacySlug+"]] and [["+legacySlug+
			"|Concurrency]]. Also Keep this label and [["+liveSlug+
			"]]. The remaining prose is long enough to stay published."+code, stored.Content)
		require.Equal(t, types.StringArray{legacySlug, liveSlug, brokenSlug}, stored.OutLinks)
		require.Equal(t, source.Version, stored.Version)
		target, err := h.svc.GetPageBySlug(ctx, backlinkTestKB, legacySlug)
		require.NoError(t, err)
		require.Equal(t, types.StringArray{source.Slug}, target.InLinks)

		fixed, err = lint.AutoFix(ctx, backlinkTestKB)
		require.NoError(t, err)
		require.Zero(t, fixed)
	})
}

func TestCreateWikiPageNormalizesSlug(t *testing.T) {
	backlinkBackends(t, func(t *testing.T, h backlinkHarness) {
		ctx := context.Background()
		page, err := h.svc.CreatePage(ctx, &types.WikiPage{
			TenantID: backlinkTestTenant, KnowledgeBaseID: backlinkTestKB,
			Slug: " Concept/Some Topic ", Title: "Some Topic", PageType: types.WikiPageTypeConcept,
		})
		require.NoError(t, err)
		require.Equal(t, "concept/some-topic", page.Slug)
		for _, slug := range []string{"concept/some-topic", "Concept/Some Topic"} {
			stored, err := h.svc.GetPageBySlug(ctx, backlinkTestKB, slug)
			require.NoError(t, err)
			require.Equal(t, page.ID, stored.ID)
		}

		_, err = h.svc.CreatePage(ctx, &types.WikiPage{
			TenantID: backlinkTestTenant, KnowledgeBaseID: backlinkTestKB, Slug: " \t ",
		})
		require.ErrorContains(t, err, "slug is required")
	})
}
