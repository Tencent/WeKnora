package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newBulkyWikiPage builds a page whose body alone is larger than a fair share
// of the default output budget.
func newBulkyWikiPage(kbID, slug string, bodyRunes int) *types.WikiPage {
	page := newTestWikiPage(kbID, slug)
	page.Summary = "summary of " + slug
	page.Content = slug + " body " + strings.Repeat("x", bodyRunes)
	return page
}

func readPageOutput(t *testing.T, ctx context.Context, tool types.Tool, slugs []string) *types.ToolResult {
	t.Helper()
	args, err := json.Marshal(map[string]any{"slugs": slugs})
	require.NoError(t, err)
	result, err := tool.Execute(ctx, args)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Success, "wiki_read_page failed: %s", result.Error)
	return result
}

// assertWellFormedPages guards the property the old tail truncation broke: the
// UI pairs <wiki_page> with </wiki_page>, so an unbalanced block is dropped
// entirely rather than shown partially.
func assertWellFormedPages(t *testing.T, output string, want int) {
	t.Helper()
	assert.Equal(t, want, strings.Count(output, "<wiki_page>"), "opening tags")
	assert.Equal(t, want, strings.Count(output, "</wiki_page>"), "closing tags")
	assert.Equal(t, want, strings.Count(output, "</content>"), "content sections")
}

// A batch read used to lose its middle pages: the pages were concatenated and
// then cut to a head/tail window by the registry, so slugs 3..5 of a 5-slug
// call disappeared without any signal to the model.
func TestWikiReadPageKeepsEveryRequestedSlugWithinBudget(t *testing.T) {
	slugs := []string{
		"concept/open-source-accessibility",
		"entity/open-source-accessibility-community-day",
		"concept/assistive-technology",
		"entity/github-multilingual-repositories-dataset",
		"concept/multilingual-ai",
	}
	pages := map[string]*types.WikiPage{}
	for _, slug := range slugs {
		pages[wikiPageKey("kb-1", slug)] = newBulkyWikiPage("kb-1", slug, 8000)
	}
	service := &fakeWikiPageService{pages: pages}
	tool := NewWikiReadPageTool(service, nil, NewWikiScopesFromKBIDs([]string{"kb-1"}), NewWikiRouteResolver())

	result := readPageOutput(t, context.Background(), tool, slugs)

	assertWellFormedPages(t, result.Output, len(slugs))
	for _, slug := range slugs {
		assert.Contains(t, result.Output, fmt.Sprintf("[[%s|%s]]", slug, slug),
			"every requested slug must still be rendered")
	}
	assert.LessOrEqual(t, utf8.RuneCountInString(result.Output), DefaultMaxToolOutput)
	assert.NotContains(t, result.Output, "<omitted_pages")
	assert.Len(t, result.Data["truncated_slugs"], len(slugs))
}

// <truncated_pages> and <omitted_pages> name one slug per line, so a wide batch
// outgrows the fixed wikiBudgetReserve the pages are rendered against. The tool
// then overshot the ceiling it was handed and the registry's fallback cut a
// head/tail window through the result, unbalancing <wiki_page> pairs — the very
// outcome the budget code exists to prevent. The ceiling is per agent
// (MaxToolOutputChars), so a batch that fits the default can still overflow a
// smaller one: the trailer's lines must be budgeted, not assumed.
func TestWikiReadPageKeepsManyTrimmedSlugsInsideTheCeiling(t *testing.T) {
	slugs := []string{
		"concept/open-source-accessibility",
		"entity/open-source-accessibility-community-day",
		"concept/assistive-technology",
		"entity/github-multilingual-repositories-dataset",
		"concept/multilingual-ai",
		"entity/github-actions-self-hosted-runner",
		"concept/open-source-license-compatibility",
		"entity/document-layout-parsing-toolkit",
		"concept/retrieval-augmented-generation",
		"entity/vector-database-benchmark-suite",
		"concept/knowledge-graph-construction",
		"entity/assistive-technology-vendor-map",
	}
	const budget = 2000 // a small per-agent ceiling, far below the default 24000
	pages := map[string]*types.WikiPage{}
	for _, slug := range slugs {
		pages[wikiPageKey("kb-1", slug)] = newBulkyWikiPage("kb-1", slug, 8000)
	}
	service := &fakeWikiPageService{pages: pages}
	args, err := json.Marshal(map[string]any{"slugs": slugs})
	require.NoError(t, err)

	// The tool is told the same ceiling the registry falls back to, and must
	// shape pages plus trailers to fit it.
	tool := NewWikiReadPageTool(service, nil, NewWikiScopesFromKBIDs([]string{"kb-1"}), NewWikiRouteResolver())
	result := readPageOutput(t, WithOutputBudget(context.Background(), budget), tool, slugs)
	truncated, _ := result.Data["truncated_slugs"].([]string)
	omitted, _ := result.Data["omitted_slugs"].([]string)
	require.Len(t, append(append([]string{}, truncated...), omitted...), len(slugs),
		"this batch must cut or drop every page")
	assert.GreaterOrEqual(t, len(truncated)+len(omitted), 8, "the trailers must name many slugs")
	assert.LessOrEqual(t, utf8.RuneCountInString(result.Output), budget,
		"the trailer's slug lines are output too and must be charged to the budget")

	// The same read through the registry must arrive with every rendered
	// <wiki_page> still paired; its head/tail fallback is what breaks them.
	registry := NewToolRegistry()
	registry.SetMaxToolOutputSize(budget)
	registry.RegisterTool(NewWikiReadPageTool(
		service, nil, NewWikiScopesFromKBIDs([]string{"kb-1"}), NewWikiRouteResolver()))
	regResult, err := registry.ExecuteTool(context.Background(), ToolWikiReadPage, args)
	require.NoError(t, err)
	require.True(t, regResult.Success, "wiki_read_page failed: %s", regResult.Error)

	assertWellFormedPages(t, regResult.Output, len(slugs)-len(omitted))
	assert.Contains(t, regResult.Output, "</truncated_pages>", "the trailer must survive whole")
	assert.Contains(t, regResult.Output, "</omitted_pages>", "the trailer must survive whole")
	for _, slug := range slugs {
		assert.Contains(t, regResult.Output, slug, "every cut or dropped slug must still be named")
	}
}

// Trimming has a floor. Below it, dropping a page by name beats rendering a
// stub, but the model must be told which slugs it still has not seen.
func TestWikiReadPageNamesOmittedPagesWhenBudgetIsTooSmall(t *testing.T) {
	slugs := []string{"concept/a", "concept/b", "concept/c", "concept/d", "concept/e"}
	pages := map[string]*types.WikiPage{}
	for _, slug := range slugs {
		pages[wikiPageKey("kb-1", slug)] = newBulkyWikiPage("kb-1", slug, 4000)
	}
	service := &fakeWikiPageService{pages: pages}
	tool := NewWikiReadPageTool(service, nil, NewWikiScopesFromKBIDs([]string{"kb-1"}), NewWikiRouteResolver())

	ctx := WithOutputBudget(context.Background(), 2000)
	result := readPageOutput(t, ctx, tool, slugs)

	omitted, ok := result.Data["omitted_slugs"].([]string)
	require.True(t, ok)
	require.NotEmpty(t, omitted, "a 2000-rune budget cannot hold five pages")

	assertWellFormedPages(t, result.Output, len(slugs)-len(omitted))
	assert.Contains(t, result.Output, "<omitted_pages")
	for _, slug := range omitted {
		assert.Contains(t, result.Output, slug)
	}
	assert.Contains(t, result.Output, "Call wiki_read_page again with fewer slugs")
}

// A page short enough to fit must not be trimmed just because a sibling in the
// same batch is huge.
func TestWikiReadPageKeepsSmallPagesIntactBesideLargeOnes(t *testing.T) {
	small := newTestWikiPage("kb-1", "concept/small")
	small.Content = "a compact body that easily fits the budget"
	service := &fakeWikiPageService{pages: map[string]*types.WikiPage{
		wikiPageKey("kb-1", "concept/small"): small,
		wikiPageKey("kb-1", "concept/huge"):  newBulkyWikiPage("kb-1", "concept/huge", 40000),
	}}
	tool := NewWikiReadPageTool(service, nil, NewWikiScopesFromKBIDs([]string{"kb-1"}), NewWikiRouteResolver())

	result := readPageOutput(t, context.Background(), tool, []string{"concept/small", "concept/huge"})

	assertWellFormedPages(t, result.Output, 2)
	assert.Contains(t, result.Output, small.Content, "the small page must survive untrimmed")
	assert.Equal(t, []string{"concept/huge"}, result.Data["truncated_slugs"])
}

// The head/tail window hides the middle of a trimmed page. Because
// wiki_write_page replaces the whole page, a rewrite based on a truncated read
// would silently drop every hidden line, so the output must say which pages
// were cut — and only those.
func TestWikiReadPageMarksTruncatedPagesInOutput(t *testing.T) {
	small := newTestWikiPage("kb-1", "concept/small")
	small.Content = "a compact body that easily fits the budget"
	service := &fakeWikiPageService{pages: map[string]*types.WikiPage{
		wikiPageKey("kb-1", "concept/small"): small,
		wikiPageKey("kb-1", "concept/huge"):  newBulkyWikiPage("kb-1", "concept/huge", 40000),
	}}
	tool := NewWikiReadPageTool(service, nil, NewWikiScopesFromKBIDs([]string{"kb-1"}), NewWikiRouteResolver())

	result := readPageOutput(t, context.Background(), tool, []string{"concept/small", "concept/huge"})

	require.Equal(t, []string{"concept/huge"}, result.Data["truncated_slugs"])
	require.Contains(t, result.Output, "<truncated_pages")
	marker := result.Output[strings.Index(result.Output, "<truncated_pages"):]
	marker = marker[:strings.Index(marker, "</truncated_pages>")]
	assert.Contains(t, marker, "concept/huge", "the trimmed page must be named")
	assert.NotContains(t, marker, "concept/small", "an intact page must not be named")
	assert.Contains(t, result.Output, "wiki_write_page",
		"the hint must warn against rewriting the page from a partial read")
}

// A read that fits the budget is the common case and must stay noise-free.
func TestWikiReadPageHasNoTruncationMarkerWhenNothingIsCut(t *testing.T) {
	service := &fakeWikiPageService{pages: map[string]*types.WikiPage{
		wikiPageKey("kb-1", "concept/small"): newTestWikiPage("kb-1", "concept/small"),
	}}
	tool := NewWikiReadPageTool(service, nil, NewWikiScopesFromKBIDs([]string{"kb-1"}), NewWikiRouteResolver())

	result := readPageOutput(t, context.Background(), tool, []string{"concept/small"})

	assert.Empty(t, result.Data["truncated_slugs"])
	assert.NotContains(t, result.Output, "<truncated_pages")
}

// Inlining a full summary for every neighbour costs one query each and used to
// consume more budget than the bodies the caller actually asked for.
func TestWikiReadPageCapsInlinedLinkSummaries(t *testing.T) {
	page := newTestWikiPage("kb-1", "concept/hub")
	pages := map[string]*types.WikiPage{wikiPageKey("kb-1", "concept/hub"): page}
	for i := 0; i < 40; i++ {
		slug := fmt.Sprintf("entity/neighbour-%02d", i)
		page.OutLinks = append(page.OutLinks, slug)
		neighbour := newTestWikiPage("kb-1", slug)
		neighbour.Summary = "NEIGHBOUR_SUMMARY " + strings.Repeat("y", 500)
		pages[wikiPageKey("kb-1", slug)] = neighbour
	}
	service := &fakeWikiPageService{pages: pages}
	tool := NewWikiReadPageTool(service, nil, NewWikiScopesFromKBIDs([]string{"kb-1"}), NewWikiRouteResolver())

	result := readPageOutput(t, context.Background(), tool, []string{"concept/hub"})

	assert.Equal(t, wikiMaxLinkSummaries, strings.Count(result.Output, "NEIGHBOUR_SUMMARY"),
		"only the first %d neighbour summaries may be inlined", wikiMaxLinkSummaries)
	for i := 0; i < 40; i++ {
		assert.Contains(t, result.Output, fmt.Sprintf("[[entity/neighbour-%02d]]", i),
			"every neighbour slug stays visible even without its summary")
	}
	assert.NotContains(t, result.Output, strings.Repeat("y", wikiLinkSummaryMaxRunes+1),
		"each inlined summary must be capped")
}

// The same slug arriving twice used to be resolved and rendered twice, spending
// budget that a distinct page needed.
func TestWikiReadPageDedupesRequestedSlugs(t *testing.T) {
	service := &fakeWikiPageService{pages: map[string]*types.WikiPage{
		wikiPageKey("kb-1", "concept/a"): newTestWikiPage("kb-1", "concept/a"),
	}}
	tool := NewWikiReadPageTool(service, nil, NewWikiScopesFromKBIDs([]string{"kb-1"}), NewWikiRouteResolver())

	args := json.RawMessage(`{"slugs":["concept/a","concept/a"],"slug":"concept/a"}`)
	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)
	require.True(t, result.Success)

	assertWellFormedPages(t, result.Output, 1)
}

// wikiPageBatch resolves one bulky page per slug for the ceiling tests below.
func wikiPageBatch(slugs []string, bodyRunes int) map[string]*types.WikiPage {
	pages := make(map[string]*types.WikiPage, len(slugs))
	for _, slug := range slugs {
		pages[wikiPageKey("kb-1", slug)] = newBulkyWikiPage("kb-1", slug, bodyRunes)
	}
	return pages
}

// readPageThroughRegistry executes the tool the way the agent does, with the
// ceiling set on the registry itself, and returns both the registry's result and
// the same call's result when the tool is executed directly with the same
// ceiling. The two must agree: the registry only falls back to its head/tail
// truncation when the tool overshot.
func readPageThroughRegistry(
	t *testing.T,
	service *fakeWikiPageService,
	slugs []string,
	budget int,
) (*types.ToolResult, *types.ToolResult) {
	t.Helper()
	args, err := json.Marshal(map[string]any{"slugs": slugs})
	require.NoError(t, err)

	direct := readPageOutput(t, WithOutputBudget(context.Background(), budget), NewWikiReadPageTool(
		service, nil, NewWikiScopesFromKBIDs([]string{"kb-1"}), NewWikiRouteResolver()), slugs)

	registry := NewToolRegistry()
	registry.SetMaxToolOutputSize(budget)
	registry.RegisterTool(NewWikiReadPageTool(
		service, nil, NewWikiScopesFromKBIDs([]string{"kb-1"}), NewWikiRouteResolver()))
	result, err := registry.ExecuteTool(context.Background(), ToolWikiReadPage, args)
	require.NoError(t, err)
	require.True(t, result.Success, "wiki_read_page failed: %s", result.Error)
	return direct, result
}

// tinyCeilingSlugs is a wide batch: 20 pages is enough for the trailer bound
// (one line per slug) to exceed these ceilings on its own, which is the shape
// where keeping the first render overshot the ceiling.
func tinyCeilingSlugs() []string {
	return []string{
		"concept/open-source-accessibility",
		"entity/open-source-accessibility-community-day",
		"concept/assistive-technology",
		"entity/github-multilingual-repositories-dataset",
		"concept/multilingual-ai",
		"entity/github-actions-self-hosted-runner",
		"concept/open-source-license-compatibility",
		"entity/document-layout-parsing-toolkit",
		"concept/retrieval-augmented-generation",
		"entity/vector-database-benchmark-suite",
		"concept/knowledge-graph-construction",
		"entity/assistive-technology-vendor-map",
		"entity/open-source-hardware-license",
		"concept/data-provenance-tracking",
		"entity/multilingual-speech-corpus",
		"concept/federated-knowledge-graph",
		"entity/assistive-technology-standards-body",
		"concept/vector-index-quantization",
		"entity/open-access-publishing-agreement",
		"concept/retrieval-evaluation-harness",
	}
}

// A ceiling this small cannot hold the pages plus the full trailers, and the
// batch-wide trailer bound is larger than the ceiling itself. Whatever the
// render chooses, it must be one result that already fits: pages paired, the
// trailers closed, and every page either shown, trimmed or counted. Handing the
// registry a blob that overshoots is what lets its head/tail fallback cut a
// <wiki_page> pair in half.
func TestWikiReadPageTinyCeilingsKeepTheResultWhole(t *testing.T) {
	slugs := tinyCeilingSlugs()
	service := &fakeWikiPageService{pages: wikiPageBatch(slugs, 8000)}

	for _, budget := range []int{800, 1000, 1200} {
		t.Run(fmt.Sprintf("ceiling-%d", budget), func(t *testing.T) {
			direct, result := readPageThroughRegistry(t, service, slugs, budget)
			output := result.Output

			assert.LessOrEqual(t, utf8.RuneCountInString(direct.Output), budget,
				"the tool must render inside the ceiling it was handed")
			assert.Equal(t, direct.Output, output,
				"the registry's head/tail fallback must not have touched the result")

			open := strings.Count(output, "<wiki_page>")
			assertWellFormedPages(t, output, open)
			for _, tag := range []string{"truncated_pages", "omitted_pages"} {
				assert.Equal(t, strings.Count(output, "<"+tag), strings.Count(output, "</"+tag+">"),
					"the %s trailer must survive whole", tag)
			}

			truncated, _ := result.Data["truncated_slugs"].([]string)
			omitted, _ := result.Data["omitted_slugs"].([]string)
			reported := append(append([]string{}, truncated...), omitted...)
			require.Len(t, reported, len(slugs), "every page must be shown, trimmed or dropped")
			assert.Equal(t, len(slugs)-open, len(omitted),
				"a page that is not rendered must be reported as omitted")

			// The trailer must still tell the model that more pages are waiting:
			// it names them, or it counts what it could not name.
			named := 0
			for _, slug := range reported {
				if strings.Contains(output, slug) {
					named++
				}
			}
			if named < len(reported) {
				assert.Contains(t, output, "more", "the trailer must count the pages it cannot name")
			}
			assert.NotEmpty(t, reported, "a 1200 rune ceiling cannot show 20 pages of 8000 runes")
		})
	}
}

// The compact trailer form is what keeps a page on screen when the full trailer
// text does not fit: at 1200 runes the batch-wide bound leaves no page room,
// while the capped lists still do.
func TestWikiReadPageCompactTrailersKeepAPageOnScreen(t *testing.T) {
	slugs := []string{
		"concept/open-source-accessibility",
		"entity/open-source-accessibility-community-day",
		"concept/assistive-technology",
		"entity/github-multilingual-repositories-dataset",
		"concept/multilingual-ai",
		"entity/github-actions-self-hosted-runner",
		"concept/open-source-license-compatibility",
		"entity/document-layout-parsing-toolkit",
		"concept/retrieval-augmented-generation",
		"entity/vector-database-benchmark-suite",
		"concept/knowledge-graph-construction",
		"entity/assistive-technology-vendor-map",
	}
	service := &fakeWikiPageService{pages: wikiPageBatch(slugs, 8000)}

	for _, budget := range []int{1000, 1200} {
		t.Run(fmt.Sprintf("ceiling-%d", budget), func(t *testing.T) {
			direct, result := readPageThroughRegistry(t, service, slugs, budget)

			assert.LessOrEqual(t, utf8.RuneCountInString(direct.Output), budget)
			assert.Equal(t, direct.Output, result.Output)
			assertWellFormedPages(t, result.Output, strings.Count(result.Output, "<wiki_page>"))
			require.GreaterOrEqual(t, strings.Count(result.Output, "<wiki_page>"), 1,
				"the compact trailers must leave room for a page")

			truncated, _ := result.Data["truncated_slugs"].([]string)
			require.NotEmpty(t, truncated)
			assert.Contains(t, result.Output, "<truncated_pages")
			assert.Contains(t, result.Output, "</truncated_pages>")
			assert.Contains(t, result.Output, "wiki_write_page",
				"the hint must still warn against rewriting a trimmed page")
			if named := strings.Count(result.Output, truncated[0]); named > 0 {
				assert.Contains(t, result.Output, "more",
					"a trailer that does not name every trimmed page must count them")
			}
		})
	}
}

// Below the compact trailers' own fixed text there is nothing left to pair, so
// the tool answers with a clipped sentence and no markup at all. The point is
// that it still answers inside the ceiling instead of handing the registry a
// blob whose head/tail fallback would leave a half-open tag behind.
func TestWikiReadPageReportsCeilingsTooSmallForAnyTrailer(t *testing.T) {
	slugs := tinyCeilingSlugs()
	service := &fakeWikiPageService{pages: wikiPageBatch(slugs, 8000)}

	for _, budget := range []int{40, 100, 160} {
		t.Run(fmt.Sprintf("ceiling-%d", budget), func(t *testing.T) {
			direct, result := readPageThroughRegistry(t, service, slugs, budget)

			assert.LessOrEqual(t, utf8.RuneCountInString(direct.Output), budget)
			assert.Equal(t, direct.Output, result.Output)
			assertWellFormedPages(t, result.Output, 0)
			assert.Equal(t, 0, strings.Count(result.Output, "<truncated_pages"))
			assert.Equal(t, 0, strings.Count(result.Output, "<omitted_pages"))
			assert.NotEmpty(t, result.Output, "the tool must still say something")
		})
	}
}
