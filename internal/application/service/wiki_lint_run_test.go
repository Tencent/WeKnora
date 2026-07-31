package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The three fakes below embed their interface so the zero value satisfies it
// and only the methods a lint run actually reaches are implemented. Anything
// else panics with a nil-method call, which is the behaviour we want: if
// ProcessRun grows a new dependency, the test says so instead of quietly
// exercising a stub.

type fakeLintKBService struct {
	interfaces.KnowledgeBaseService
	wikiEnabled bool
}

func (f *fakeLintKBService) GetKnowledgeBaseByIDOnly(
	_ context.Context, id string,
) (*types.KnowledgeBase, error) {
	kb := &types.KnowledgeBase{ID: id}
	kb.IndexingStrategy.WikiEnabled = f.wikiEnabled
	return kb, nil
}

type fakeLintWikiService struct {
	interfaces.WikiPageService
	pages []*types.WikiPage
	stats *types.WikiStats
	slugs []string
	// cursorCalls counts walks so a test can prove the scan streams in windows
	// rather than pulling the whole KB at once.
	cursorCalls int
}

func (f *fakeLintWikiService) GetStats(_ context.Context, _ string) (*types.WikiStats, error) {
	return f.stats, nil
}

func (f *fakeLintWikiService) ListAllSlugs(_ context.Context, _ string) ([]string, error) {
	return f.slugs, nil
}

// ListPagesCursor pages through f.pages using the page index as the cursor,
// mirroring the id-asc contract of the real implementation.
func (f *fakeLintWikiService) ListPagesCursor(
	_ context.Context, _ string, cursor string, limit int,
) ([]*types.WikiPage, string, error) {
	f.cursorCalls++
	start := 0
	if cursor != "" {
		if _, err := fmt.Sscanf(cursor, "%d", &start); err != nil {
			return nil, "", err
		}
	}
	if start >= len(f.pages) {
		return nil, "", nil
	}
	end := min(start+limit, len(f.pages))
	next := ""
	if end < len(f.pages) {
		next = fmt.Sprintf("%d", end)
	}
	return f.pages[start:end], next, nil
}

type fakeLintRepo struct {
	interfaces.WikiPageRepository
	run *types.WikiLintRun
	// batches records each upsert window so a test can assert on batching
	// itself, not merely on the union of persisted rows.
	batches      [][]*types.WikiPageIssue
	progress     []int
	reconciled   []string
	upsertErr    error
	upsertErrsAt int
}

func (f *fakeLintRepo) GetLintRun(_ context.Context, _, _ string) (*types.WikiLintRun, error) {
	return f.run, nil
}

func (f *fakeLintRepo) UpdateLintRun(_ context.Context, run *types.WikiLintRun) error {
	f.progress = append(f.progress, run.Progress)
	return nil
}

func (f *fakeLintRepo) UpsertLintIssues(_ context.Context, issues []*types.WikiPageIssue) error {
	if f.upsertErr != nil && len(f.batches) == f.upsertErrsAt {
		return f.upsertErr
	}
	batch := make([]*types.WikiPageIssue, len(issues))
	copy(batch, issues)
	f.batches = append(f.batches, batch)
	return nil
}

func (f *fakeLintRepo) ResolveMissingLintIssues(
	_ context.Context, _, runID string, _ time.Time,
) error {
	f.reconciled = append(f.reconciled, runID)
	return nil
}

func (f *fakeLintRepo) persisted() []*types.WikiPageIssue {
	var all []*types.WikiPageIssue
	for _, batch := range f.batches {
		all = append(all, batch...)
	}
	return all
}

// orphanPages builds n pages that each trip exactly one durable rule
// (orphan_page) plus one advisory rule, so a test can count both classes.
func orphanPages(n int) []*types.WikiPage {
	pages := make([]*types.WikiPage, 0, n)
	for i := 0; i < n; i++ {
		pages = append(pages, &types.WikiPage{
			ID: fmt.Sprintf("page-%03d", i), Slug: fmt.Sprintf("entity/p%03d", i),
			Title: fmt.Sprintf("Page%03d", i), PageType: types.WikiPageTypeEntity,
			Version: 1,
			// Long enough not to also trip the empty-content rule.
			Content: "This page body is comfortably longer than the empty-content threshold of fifty runes.",
		})
	}
	return pages
}

func newLintRunFixture(pages []*types.WikiPage) (*WikiLintService, *fakeLintRepo) {
	slugs := make([]string, 0, len(pages))
	for _, page := range pages {
		slugs = append(slugs, page.Slug)
	}
	wiki := &fakeLintWikiService{
		pages: pages, slugs: slugs,
		stats: &types.WikiStats{TotalPages: int64(len(pages)), TotalLinks: 10},
	}
	repo := &fakeLintRepo{run: &types.WikiLintRun{
		ID: "run-1", TenantID: 7, KnowledgeBaseID: "kb-1", Status: "queued",
	}}
	svc := NewWikiLintService(wiki, &fakeLintKBService{wikiEnabled: true}, nil, repo)
	return svc, repo
}

// TestProcessRunStreamsFindingsIntoBatches is the headline test for the
// streaming rewrite. The scan must hand findings to the repository in
// wikiLintUpsertBatch-sized windows instead of collecting them all first — that
// collection was unbounded, and on a large knowledge base the finding set, not
// the page walk, was what exhausted memory.
func TestProcessRunStreamsFindingsIntoBatches(t *testing.T) {
	pageCount := wikiLintUpsertBatch*2 + 37
	svc, repo := newLintRunFixture(orphanPages(pageCount))

	require.NoError(t, svc.ProcessRun(context.Background(), WikiLintTaskPayload{
		TenantID: 7, KnowledgeBaseID: "kb-1", RunID: "run-1",
	}))

	require.Len(t, repo.batches, 3, "findings must arrive in windows, not one final dump")
	assert.Len(t, repo.batches[0], wikiLintUpsertBatch)
	assert.Len(t, repo.batches[1], wikiLintUpsertBatch)
	assert.Len(t, repo.batches[2], 37, "the tail must be flushed after the walk ends")
	assert.Len(t, repo.persisted(), pageCount)

	assert.Equal(t, "completed", repo.run.Status)
	assert.Equal(t, 100, repo.run.Progress)
	assert.Equal(t, pageCount, repo.run.FindingCount)
	assert.Empty(t, repo.run.ErrorMessage)
	assert.Equal(t, []string{"run-1"}, repo.reconciled,
		"absence reconciliation runs exactly once, after every write landed")
}

// TestProcessRunPersistsOnlyDurableFindings pins the decision that keeps the
// problem centre usable: advisory (info) findings fire once per (page,
// mentioned entity) pair, so persisting them would bury the actual defects
// under a quadratic pile of suggestions.
func TestProcessRunPersistsOnlyDurableFindings(t *testing.T) {
	// Two entity pages that mention each other's titles without linking, so
	// the advisory cross-reference rule fires in both directions on top of the
	// orphan findings.
	pages := []*types.WikiPage{
		{
			ID: "page-a", Slug: "entity/alpha", Title: "Alpha",
			PageType: types.WikiPageTypeEntity, Version: 1,
			Content: "Alpha discusses Beta at length without ever linking to it, and is long enough to pass.",
		},
		{
			ID: "page-b", Slug: "entity/beta", Title: "Beta",
			PageType: types.WikiPageTypeEntity, Version: 1,
			Content: "Beta discusses Alpha at length without ever linking to it, and is long enough to pass.",
		},
	}
	svc, repo := newLintRunFixture(pages)

	// The synchronous report sees both classes.
	report, err := svc.RunLint(context.Background(), "kb-1")
	require.NoError(t, err)
	byType := map[WikiLintIssueType]int{}
	for _, issue := range report.Issues {
		byType[issue.Type]++
	}
	assert.Equal(t, 2, byType[LintIssueOrphanPage])
	assert.Equal(t, 2, byType[LintIssueMissingCrossRef],
		"advisory findings still belong in the human-facing report")

	// The durable run keeps only the defects.
	require.NoError(t, svc.ProcessRun(context.Background(), WikiLintTaskPayload{
		TenantID: 7, KnowledgeBaseID: "kb-1", RunID: "run-1",
	}))
	persisted := repo.persisted()
	require.Len(t, persisted, 2)
	for _, issue := range persisted {
		assert.Equal(t, string(LintIssueOrphanPage), issue.IssueType)
		assert.Equal(t, types.WikiIssueSourceLint, issue.Source)
		assert.Equal(t, types.WikiIssueStatusOpen, issue.Status)
		assert.Equal(t, "run-1", issue.LastSeenRunID)
		assert.NotEmpty(t, issue.Fingerprint)
	}
	assert.Equal(t, 2, repo.run.FindingCount,
		"finding_count reports what was persisted, matching what the UI can open")
}

// TestProcessRunRecordsEvidenceAndRuleVersion covers the round-trip the repair
// postconditions depend on: a rule that needs a counterpart can only verify its
// finding if the detector wrote that counterpart into evidence.
func TestProcessRunRecordsEvidenceAndRuleVersion(t *testing.T) {
	pages := []*types.WikiPage{{
		ID: "page-a", Slug: "entity/alpha", Title: "Alpha",
		PageType: types.WikiPageTypeEntity, Version: 4,
		InLinks:  types.StringArray{"entity/somewhere"},
		OutLinks: types.StringArray{"entity/ghost"},
		Content:  "Alpha links somewhere that no longer exists, and this body clears the length threshold.",
	}}
	svc, repo := newLintRunFixture(pages)

	require.NoError(t, svc.ProcessRun(context.Background(), WikiLintTaskPayload{
		TenantID: 7, KnowledgeBaseID: "kb-1", RunID: "run-1",
	}))

	persisted := repo.persisted()
	require.Len(t, persisted, 1)
	issue := persisted[0]
	assert.Equal(t, string(LintIssueBrokenLink), issue.IssueType)
	assert.Equal(t, types.WikiIssueRepairDeterministic, issue.RepairMode)
	assert.Equal(t, "high", issue.Severity)
	assert.Equal(t, 4, issue.DetectedPageVersion)
	assert.Equal(t, uint64(7), issue.TenantID)

	var evidence map[string]interface{}
	require.NoError(t, json.Unmarshal(issue.Evidence, &evidence))
	assert.Equal(t, "entity/ghost", evidence["target_slug"])
	assert.Equal(t, wikiLintRuleVersion, evidence["rule_version"])
}

// TestProcessRunDoesNotReconcileWhenAWriteFails is the invariant that makes
// closing issues by absence safe. Reconciliation deletes information — it
// closes anything the run did not report — so it may only follow a run that
// observed and stored everything.
func TestProcessRunDoesNotReconcileWhenAWriteFails(t *testing.T) {
	svc, repo := newLintRunFixture(orphanPages(wikiLintUpsertBatch + 5))
	repo.upsertErr = errors.New("connection reset")
	repo.upsertErrsAt = 0 // fail the very first window

	err := svc.ProcessRun(context.Background(), WikiLintTaskPayload{
		TenantID: 7, KnowledgeBaseID: "kb-1", RunID: "run-1",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "persist lint findings")

	assert.Empty(t, repo.reconciled, "a partial run must never close issues by absence")
	assert.Equal(t, "failed", repo.run.Status)
	assert.Contains(t, repo.run.ErrorMessage, "connection reset")
}

// TestProcessRunPublishesCoarseProgress checks the run stays observable without
// turning every page window into a database write. The UI polls this value, so
// it has to move; the point of the 5%-step throttle is that the number of
// writes is bounded by the progress band rather than by the size of the KB.
func TestProcessRunPublishesCoarseProgress(t *testing.T) {
	windowsPerPass := 20
	svc, repo := newLintRunFixture(orphanPages(lintCursorBatch * windowsPerPass))
	wiki := svc.wikiService.(*fakeLintWikiService)

	require.NoError(t, svc.ProcessRun(context.Background(), WikiLintTaskPayload{
		TenantID: 7, KnowledgeBaseID: "kb-1", RunID: "run-1",
	}))

	assert.Equal(t, 5, repo.progress[0], "the run opens at the queued-to-running boundary")
	assert.Equal(t, 100, repo.progress[len(repo.progress)-1])
	for i := 1; i < len(repo.progress); i++ {
		assert.GreaterOrEqual(t, repo.progress[i], repo.progress[i-1],
			"progress must never go backwards")
	}

	// The band runs 5-95 in 5-point steps, so at most 19 intermediate writes
	// plus the opening 5 and the closing 100 — however many page windows the
	// walk took.
	assert.Equal(t, windowsPerPass*2, wiki.cursorCalls)
	assert.LessOrEqual(t, len(repo.progress), 21)
	assert.Less(t, len(repo.progress), wiki.cursorCalls,
		"progress writes are throttled, not one per page window")
}

// TestProcessRunRejectsNonWikiKnowledgeBase keeps a lint run from silently
// reporting a clean bill of health for a KB that has no wiki at all.
func TestProcessRunRejectsNonWikiKnowledgeBase(t *testing.T) {
	svc, repo := newLintRunFixture(orphanPages(1))
	svc.kbService = &fakeLintKBService{wikiEnabled: false}

	err := svc.ProcessRun(context.Background(), WikiLintTaskPayload{
		TenantID: 7, KnowledgeBaseID: "kb-1", RunID: "run-1",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a wiki type")
	assert.Equal(t, "failed", repo.run.Status)
	assert.Empty(t, repo.reconciled)
}
