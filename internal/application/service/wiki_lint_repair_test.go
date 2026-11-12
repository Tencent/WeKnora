package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestWikiAutoFixRepairsBrokenLinksBeforeStripping(t *testing.T) {
	backlinkBackends(t, func(t *testing.T, h backlinkHarness) {
		ctx := context.Background()
		const legacy = "concept/bingfa-kuorong-tunTu"
		const dead = "concept/bingfa-kuorong-tuntu"
		const live = "concept/database"
		code := "\n\n```md\n[[" + dead + "]]\n```"
		body := "These references should survive automatic lint repair. [[" + dead +
			"]] and [[" + live + "]]. [[" + " Concept/Missing |Keep this label]]" +
			"\n\n| Reference |\n| --- |\n| [[" + dead + "\\|Concurrency]] |" + code
		source := seedBacklinkPage(t, h, "summary/source", body, nil, []string{dead, "concept/stale-edge"})
		for _, slug := range []string{legacy, live, "entity/bingfa-kuorong-tuntu"} {
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
		require.Equal(t, "These references should survive automatic lint repair. [["+legacy+
			"]] and [["+live+"]]. Keep this label"+
			"\n\n| Reference |\n| --- |\n| [["+legacy+"\\|Concurrency]] |"+code, stored.Content)
		require.Equal(t, types.StringArray{legacy, live}, stored.OutLinks)
		require.Equal(t, source.Version, stored.Version)
		target, err := h.svc.GetPageBySlug(ctx, backlinkTestKB, legacy)
		require.NoError(t, err)
		require.Equal(t, types.StringArray{source.Slug}, target.InLinks)

		fixed, err = lint.AutoFix(ctx, backlinkTestKB)
		require.NoError(t, err)
		require.Zero(t, fixed)
	})
}

func TestCreateWikiPageNormalizesNewSlug(t *testing.T) {
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
