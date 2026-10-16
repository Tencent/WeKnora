package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

// lintWikiStub feeds RunLint one fixed page window. The real cursor walk is
// covered by the repository tests; the drift check only needs stable inputs.
type lintWikiStub struct {
	interfaces.WikiPageService
	pages []*types.WikiPage
}

func (s *lintWikiStub) GetStats(_ context.Context, _ string) (*types.WikiStats, error) {
	return &types.WikiStats{TotalPages: int64(len(s.pages))}, nil
}

func (s *lintWikiStub) ListAllSlugs(_ context.Context, _ string) ([]string, error) {
	slugs := make([]string, 0, len(s.pages))
	for _, page := range s.pages {
		slugs = append(slugs, page.Slug)
	}
	return slugs, nil
}

func (s *lintWikiStub) ListPagesCursor(
	_ context.Context, _ string, _ string, _ int,
) ([]*types.WikiPage, string, error) {
	return s.pages, "", nil
}

type lintKBStub struct {
	interfaces.KnowledgeBaseService
	kb *types.KnowledgeBase
}

func (s *lintKBStub) GetKnowledgeBaseByIDOnly(_ context.Context, _ string) (*types.KnowledgeBase, error) {
	return s.kb, nil
}

// lintChunkRepoStub records the batch it was asked for so a test can prove the
// window's chunk refs were fetched once, de-duplicated.
type lintChunkRepoStub struct {
	interfaces.ChunkRepository
	chunks map[string]*types.Chunk
	calls  int
	ids    []string
}

func (s *lintChunkRepoStub) ListChunksByID(
	_ context.Context, _ uint64, ids []string,
) ([]*types.Chunk, error) {
	s.calls++
	s.ids = append([]string(nil), ids...)
	out := make([]*types.Chunk, 0, len(ids))
	for _, id := range ids {
		if chunk, ok := s.chunks[id]; ok {
			out = append(out, chunk)
		}
	}
	return out, nil
}

// newLintDriftHarness wires a lint service over the stub KB and page set.
func newLintDriftHarness(
	pages []*types.WikiPage, chunks map[string]*types.Chunk,
) (*WikiLintService, *lintChunkRepoStub) {
	repo := &lintChunkRepoStub{chunks: chunks}
	svc := NewWikiLintService(
		&lintWikiStub{pages: pages},
		&lintKBStub{kb: &types.KnowledgeBase{
			ID:               "kb-drift",
			TenantID:         7,
			IndexingStrategy: types.IndexingStrategy{WikiEnabled: true},
		}},
		nil,
		repo,
	)
	return svc, repo
}

// contentDriftIssues narrows a report to the issue type under test.
func contentDriftIssues(t *testing.T, report *WikiLintReport) []WikiLintIssue {
	t.Helper()
	drift := make([]WikiLintIssue, 0, 2)
	for _, issue := range report.Issues {
		if issue.Type == LintIssueContentDrift {
			drift = append(drift, issue)
		}
	}
	return drift
}

// A page that dropped most of its source table rows is reported, with the
// severity/autofix flags the other lint checks use.
func TestRunLintReportsContentDriftAgainstSourceChunks(t *testing.T) {
	page := &types.WikiPage{
		TenantID: 7, KnowledgeBaseID: "kb-drift", Slug: "entity/pmp", Title: "PMP",
		PageType:  types.WikiPageTypeEntity,
		Content:   ledger("PMP", ledgerHolders(3)...),
		ChunkRefs: types.StringArray{"chunk-1"},
	}
	svc, repo := newLintDriftHarness([]*types.WikiPage{page}, map[string]*types.Chunk{
		"chunk-1": {ID: "chunk-1", TenantID: 7, Content: ledger("PMP", ledgerHolders(6)...)},
	})

	report, err := svc.RunLint(context.Background(), "kb-drift")
	require.NoError(t, err)

	drift := contentDriftIssues(t, report)
	require.Len(t, drift, 1)
	require.Equal(t, SeverityWarning, drift[0].Severity)
	require.Equal(t, "entity/pmp", drift[0].PageSlug)
	require.False(t, drift[0].AutoFixable)
	require.Contains(t, drift[0].Description, "missing 3 of 6 table rows")
	require.Equal(t, 1, repo.calls, "the window's chunks are fetched in one batch")
}

// A complete page is not reported, however much its formatting moved.
func TestRunLintKeepsContentDriftQuietForACompletePage(t *testing.T) {
	page := &types.WikiPage{
		TenantID: 7, KnowledgeBaseID: "kb-drift", Slug: "entity/pmp", Title: "PMP",
		PageType:  types.WikiPageTypeEntity,
		Content:   reorderedLedger("PMP", ledgerHolders(6)),
		ChunkRefs: types.StringArray{"chunk-1"},
	}
	svc, _ := newLintDriftHarness([]*types.WikiPage{page}, map[string]*types.Chunk{
		"chunk-1": {ID: "chunk-1", TenantID: 7, Content: ledger("PMP", ledgerHolders(6)...)},
	})

	report, err := svc.RunLint(context.Background(), "kb-drift")
	require.NoError(t, err)
	require.Empty(t, contentDriftIssues(t, report))
}

// Pages without chunk refs have no precise source to compare against, so they
// are skipped — and with nothing to resolve, the chunk repo is never touched.
func TestRunLintSkipsContentDriftForPagesWithoutChunkRefs(t *testing.T) {
	page := &types.WikiPage{
		TenantID: 7, KnowledgeBaseID: "kb-drift", Slug: "entity/pmp", Title: "PMP",
		PageType: types.WikiPageTypeEntity,
		Content:  ledger("PMP", ledgerHolders(1)...),
	}
	svc, repo := newLintDriftHarness([]*types.WikiPage{page}, nil)

	report, err := svc.RunLint(context.Background(), "kb-drift")
	require.NoError(t, err)
	require.Empty(t, contentDriftIssues(t, report))
	require.Zero(t, repo.calls)
}

// Two pages citing the same chunk (and a chunk no page cites) still cost one
// query, and the batch never repeats an id.
func TestRunLintBatchesAndDedupesSourceChunksAcrossPages(t *testing.T) {
	shared := ledger("PMP", ledgerHolders(6)...)
	pages := []*types.WikiPage{
		{
			TenantID: 7, KnowledgeBaseID: "kb-drift", Slug: "entity/a", Title: "A",
			PageType:  types.WikiPageTypeEntity,
			Content:   ledger("PMP", ledgerHolders(2)...),
			ChunkRefs: types.StringArray{"chunk-shared", "chunk-b", "chunk-shared"},
		},
		{
			TenantID: 7, KnowledgeBaseID: "kb-drift", Slug: "entity/b", Title: "B",
			PageType:  types.WikiPageTypeEntity,
			Content:   shared,
			ChunkRefs: types.StringArray{"chunk-shared"},
		},
	}
	svc, repo := newLintDriftHarness(pages, map[string]*types.Chunk{
		"chunk-shared": {ID: "chunk-shared", TenantID: 7, Content: shared},
		"chunk-b":      {ID: "chunk-b", TenantID: 7, Content: ledger("PMP", ledgerHolders(1)...)},
	})

	report, err := svc.RunLint(context.Background(), "kb-drift")
	require.NoError(t, err)

	require.Equal(t, 1, repo.calls)
	require.ElementsMatch(t, []string{"chunk-shared", "chunk-b"}, repo.ids)
	drift := contentDriftIssues(t, report)
	require.Len(t, drift, 1, "only page A drifted")
	require.Equal(t, "entity/a", drift[0].PageSlug)
}

// reorderedLedger re-emits the same identities with a renamed header, a styled
// delimiter, reordered rows and swapped non-identity columns. The row identity
// is the first non-empty cell — the holder — so every row is still present.
func reorderedLedger(title string, holders []string) string {
	body := "# " + title + "\n\n" +
		"| 持证人 | 有效期止 | 证书编号 |\n|:---|:--:|---:|\n"
	for i := len(holders) - 1; i >= 0; i-- {
		body += "| **" + holders[i] + "** | 2099-01-01 | CERT-2020 |\n"
	}
	return body
}

// driftChunk wraps source content in the chunk shape the helper reads.
func driftChunk(content string) *types.Chunk {
	return &types.Chunk{Content: content}
}

// A source set of ten rows whose page keeps seven loses 30% — above the guard's
// 20% tolerance — and the three missing identities are named in sorted order.
func TestWikiLintContentDriftFindsMissingSourceRows(t *testing.T) {
	holders := ledgerHolders(10)
	source := []*types.Chunk{driftChunk(ledger("PMP", holders...))}
	page := ledger("PMP", holders[:7]...)

	missing, total := wikiLintContentDrift(page, source)
	require.Equal(t, 10, total)
	require.Equal(t, holders[7:], missing)
	require.True(t, wikiLintIsContentDrift(missing, total))
}

// A page that keeps every row is not drift, even when its header, delimiter
// style, row order and non-identity columns all changed.
func TestWikiLintContentDriftAcceptsACompletePage(t *testing.T) {
	holders := ledgerHolders(6)
	source := []*types.Chunk{driftChunk(ledger("PMP", holders...))}

	missing, total := wikiLintContentDrift(reorderedLedger("PMP 证书", holders), source)
	require.Equal(t, 6, total)
	require.Empty(t, missing)
	require.False(t, wikiLintIsContentDrift(missing, total))
}

// Four source rows are below the minimum sample: a page that keeps one of them
// is a 75% "loss" that no threshold can read, so no issue is raised.
func TestWikiLintContentDriftIgnoresTinySources(t *testing.T) {
	holders := ledgerHolders(4)
	source := []*types.Chunk{driftChunk(ledger("PMP", holders...))}

	missing, total := wikiLintContentDrift(ledger("PMP", holders[:1]...), source)
	require.Equal(t, 4, total)
	require.Len(t, missing, 3)
	require.False(t, wikiLintIsContentDrift(missing, total), "below the minimum source size")
}

// A page with no table at all against a tabular source reports every identity.
func TestWikiLintContentDriftReportsEveryRowWhenThePageHasNoTable(t *testing.T) {
	holders := ledgerHolders(6)
	source := []*types.Chunk{driftChunk(ledger("PMP", holders...))}

	missing, total := wikiLintContentDrift("just prose, no tables here", source)
	require.Equal(t, 6, total)
	require.Equal(t, holders, missing)
	require.True(t, wikiLintIsContentDrift(missing, total))
}

// The source side is a UNION across chunks: one identity carried by two chunks
// counts once, so overlapping chunks cannot inflate the denominator and mask a
// loss.
func TestWikiLintContentDriftUnionsIdentitiesAcrossChunks(t *testing.T) {
	holders := ledgerHolders(6)
	source := []*types.Chunk{
		driftChunk(ledger("PMP", holders[:4]...)),
		driftChunk(ledger("PMP", holders[2:]...)),
	}

	missing, total := wikiLintContentDrift(ledger("PMP", holders...), source)
	require.Equal(t, 6, total, "overlap between chunks is counted once")
	require.Empty(t, missing)

	missing, total = wikiLintContentDrift(ledger("PMP", holders[:3]...), source)
	require.Equal(t, 6, total)
	require.Equal(t, holders[3:], missing)
}

// The threshold is the write guard's: exactly 20% is tolerated, more is not,
// and a source below the minimum sample is never drift.
func TestWikiLintIsContentDriftThresholdBoundary(t *testing.T) {
	require.False(t, wikiLintIsContentDrift(nil, 0), "an empty source cannot drift")
	require.False(t, wikiLintIsContentDrift([]string{"a"}, 4), "below the minimum source size")
	require.False(t, wikiLintIsContentDrift([]string{"a"}, 5), "exactly 20% is tolerated")
	require.True(t, wikiLintIsContentDrift([]string{"a", "b"}, 5), "40% is not")
}

// Over the per-window chunk budget the check is skipped wholesale: no query is
// issued and no partial batch can under-count a page's sources.
func TestWikiLintContentDriftSkipsAWindowAboveTheChunkBudget(t *testing.T) {
	refs := make(types.StringArray, 0, wikiLintDriftMaxChunksPerWindow+1)
	for i := 0; i <= wikiLintDriftMaxChunksPerWindow; i++ {
		refs = append(refs, fmt.Sprintf("chunk-%d", i))
	}
	page := &types.WikiPage{
		TenantID: 7, KnowledgeBaseID: "kb-drift", Slug: "entity/pmp", Title: "PMP",
		PageType: types.WikiPageTypeEntity, Content: "prose", ChunkRefs: refs,
	}
	repo := &lintChunkRepoStub{}
	svc := &WikiLintService{chunkRepo: repo}

	require.Empty(t, svc.wikiLintContentDriftIssues(context.Background(), 7, []*types.WikiPage{page}))
	require.Zero(t, repo.calls)
}
