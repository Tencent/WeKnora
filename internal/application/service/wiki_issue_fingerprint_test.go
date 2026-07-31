package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupWikiIssueService(t *testing.T) (*wikiPageService, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&types.WikiFolder{}, &types.WikiPage{}, &types.WikiPageRevision{},
		&types.WikiPageIssue{}, &types.WikiRepairAttempt{}, &types.WikiLintRun{},
	))
	repo := repository.NewWikiPageRepository(db)
	return NewWikiPageService(repo, nil, nil, nil, nil).(*wikiPageService), db
}

// TestCreateIssueKeepsDistinctAgentFindingsOnOnePage covers the silent-merge
// regression: two mixed_entities findings on the same page used to share a
// fingerprint (empty identity) and the second flag overwrote the first.
func TestCreateIssueKeepsDistinctAgentFindingsOnOnePage(t *testing.T) {
	svc, _ := setupWikiIssueService(t)
	ctx := context.Background()
	const kbID = "kb-agent-fp"

	first, err := svc.CreateIssue(ctx, &types.WikiPageIssue{
		TenantID: 1, KnowledgeBaseID: kbID, PageID: "page-1", Slug: "entity/acme",
		IssueType: "mixed_entities", Description: "Page mixes Acme Widget and Acme Cloud.",
		Source: types.WikiIssueSourceAgent,
	})
	require.NoError(t, err)

	second, err := svc.CreateIssue(ctx, &types.WikiPageIssue{
		TenantID: 1, KnowledgeBaseID: kbID, PageID: "page-1", Slug: "entity/acme",
		IssueType: "mixed_entities", Description: "Page mixes Acme Widget and Acme Mobile.",
		Source: types.WikiIssueSourceAgent,
	})
	require.NoError(t, err)

	assert.NotEqual(t, first.Fingerprint, second.Fingerprint)
	assert.NotEqual(t, first.ID, second.ID)

	listed, err := svc.ListIssues(ctx, kbID, "entity/acme", "actionable")
	require.NoError(t, err)
	require.Len(t, listed, 2)
}

// TestCreateIssueUpsertsIdenticalAgentFinding pins the complementary half of
// the fingerprint contract: re-flagging the same prose claim must update the
// existing row rather than grow a duplicate.
func TestCreateIssueUpsertsIdenticalAgentFinding(t *testing.T) {
	svc, _ := setupWikiIssueService(t)
	ctx := context.Background()
	const kbID = "kb-agent-upsert"

	first, err := svc.CreateIssue(ctx, &types.WikiPageIssue{
		TenantID: 1, KnowledgeBaseID: kbID, PageID: "page-1", Slug: "entity/acme",
		IssueType: "out_of_date", Description: "Pricing section cites 2023 numbers.",
		Source: types.WikiIssueSourceAgent,
	})
	require.NoError(t, err)

	again, err := svc.CreateIssue(ctx, &types.WikiPageIssue{
		TenantID: 1, KnowledgeBaseID: kbID, PageID: "page-1", Slug: "entity/acme",
		IssueType: "out_of_date", Description: "Pricing section cites 2023 numbers.",
		Source: types.WikiIssueSourceAgent,
	})
	require.NoError(t, err)

	assert.Equal(t, first.Fingerprint, again.Fingerprint)
	listed, err := svc.ListIssues(ctx, kbID, "entity/acme", "actionable")
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, first.ID, listed[0].ID)
	assert.Equal(t, 2, listed[0].OccurrenceCount)
}

// TestDeterministicRepairAvailableEscalatesWhenTargetIsGone is the pre-flight
// check behind StartIssueRepair: a broken link to a page that simply does not
// exist must not look "deterministic-fixable", or the UI dead-ends in a
// failed attempt with no agent session.
func TestDeterministicRepairAvailableEscalatesWhenTargetIsGone(t *testing.T) {
	wikiSvc, _ := setupWikiIssueService(t)
	ctx := context.Background()
	const kbID = "kb-det"
	now := time.Now()

	page := &types.WikiPage{
		ID: uuid.New().String(), TenantID: 1, KnowledgeBaseID: kbID,
		Slug: "entity/source", Title: "Source", PageType: types.WikiPageTypeEntity,
		Status: types.WikiPageStatusPublished, Version: 1,
		Content:   "See [[entity/deleted-target|Deleted]] for details, long enough body here.",
		OutLinks:  types.StringArray{"entity/deleted-target"},
		CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, wikiSvc.repo.Create(ctx, page))

	// A live sibling so RepairContentLinks has a candidate pool — but nothing
	// similar enough to the deleted target for a confident rewrite.
	require.NoError(t, wikiSvc.repo.Create(ctx, &types.WikiPage{
		ID: uuid.New().String(), TenantID: 1, KnowledgeBaseID: kbID,
		Slug: "entity/unrelated", Title: "Unrelated", PageType: types.WikiPageTypeEntity,
		Status: types.WikiPageStatusPublished, Version: 1,
		Content:   "unrelated body that clears empty-content checks with room to spare",
		CreatedAt: now, UpdatedAt: now,
	}))

	lintSvc := NewWikiLintService(wikiSvc, nil, nil, wikiSvc.repo)
	issue := &types.WikiPageIssue{
		IssueType: string(LintIssueBrokenLink), KnowledgeBaseID: kbID, Slug: page.Slug,
		RepairMode: types.WikiIssueRepairDeterministic,
	}
	assert.False(t, lintSvc.DeterministicRepairAvailable(ctx, issue),
		"a simply-missing target must escalate to the agent")

	_, _, err := lintSvc.planDeterministicRepair(ctx, issue)
	require.ErrorIs(t, err, ErrWikiNoDeterministicRepair)
}

// TestDeterministicRepairAvailableWhenMangledSlugExists covers the positive
// path: a one-character mangle of a live UUID slug is what deterministic repair
// is for, so the pre-flight check and the repair itself must both see it.
func TestDeterministicRepairAvailableWhenMangledSlugExists(t *testing.T) {
	wikiSvc, _ := setupWikiIssueService(t)
	ctx := context.Background()
	const kbID = "kb-mangle"
	now := time.Now()

	realSummary := "summary/07a20bb1-a662-47cf-9929-06fb5d5b5b5e"
	require.NoError(t, wikiSvc.repo.Create(ctx, &types.WikiPage{
		ID: uuid.New().String(), TenantID: 1, KnowledgeBaseID: kbID,
		Slug: realSummary, Title: "Weknora 试错记录.md - Summary",
		PageType: types.WikiPageTypeSummary, Status: types.WikiPageStatusPublished, Version: 1,
		Content:   "summary body long enough to clear the empty threshold with ease",
		CreatedAt: now, UpdatedAt: now,
	}))
	page := &types.WikiPage{
		ID: uuid.New().String(), TenantID: 1, KnowledgeBaseID: kbID,
		Slug: "synthesis/notes", Title: "Notes", PageType: types.WikiPageTypeSynthesis,
		Status: types.WikiPageStatusPublished, Version: 1,
		Content:   "See [[summary/07a20bb1-a662-47cf-9929-06fb14d5b14b14e|Weknora 试错记录.md - Summary]].",
		CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, wikiSvc.repo.Create(ctx, page))

	lintSvc := NewWikiLintService(wikiSvc, nil, nil, wikiSvc.repo)
	issue := &types.WikiPageIssue{
		IssueType: string(LintIssueBrokenLink), KnowledgeBaseID: kbID, Slug: page.Slug,
		RepairMode: types.WikiIssueRepairDeterministic,
	}
	assert.True(t, lintSvc.DeterministicRepairAvailable(ctx, issue))
}
