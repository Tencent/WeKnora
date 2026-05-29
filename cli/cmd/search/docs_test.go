package search

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

// fakeDocsSearchSvc scripts paginated ListKnowledgeWithFilter responses.
// Pages are indexed 1-based; items keyed by page. The last-received filter
// is captured so tests can assert opts.Query was threaded as filter.Keyword.
type fakeDocsSearchSvc struct {
	pages      map[int][]sdk.Knowledge
	total      int64
	err        error
	calls      []int // page numbers requested, for assertions
	lastFilter sdk.KnowledgeListFilter
}

func (f *fakeDocsSearchSvc) ListKnowledgeWithFilter(_ context.Context, kbID string, page, pageSize int, filter sdk.KnowledgeListFilter) ([]sdk.Knowledge, int64, error) {
	f.calls = append(f.calls, page)
	f.lastFilter = filter
	if f.err != nil {
		return nil, 0, f.err
	}
	return f.pages[page], f.total, nil
}

func TestDocsSearch_Substring(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	// Server applies the keyword filter pre-pagination; the fake simulates
	// that by only returning the matching items (d1/d3, not d2).
	svc := &fakeDocsSearchSvc{
		pages: map[int][]sdk.Knowledge{
			1: {
				{ID: "d1", Title: "Q3 Forecast", FileName: "q3.pdf", UpdatedAt: mustTime(t, "2026-05-10T00:00:00Z")},
				{ID: "d3", Title: "Q3 retro", FileName: "retro.pdf", UpdatedAt: mustTime(t, "2026-05-11T00:00:00Z")},
			},
		},
		total: 2,
	}
	require.NoError(t, runDocsSearch(context.Background(), &DocsSearchOptions{Query: "q3", KBID: "kb1", Limit: 20, PageSize: docsPageSize, AllPages: true}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc))
	assert.Equal(t, "q3", svc.lastFilter.Keyword, "query must be threaded as filter.Keyword")
	got := out.String()
	assert.Contains(t, got, "d1")
	assert.Contains(t, got, "d3")
	assert.NotContains(t, got, "d2")
}

func TestDocsSearch_MatchesFileName(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeDocsSearchSvc{
		pages: map[int][]sdk.Knowledge{1: {{ID: "d1", Title: "Untitled", FileName: "report.pdf"}}},
		total: 1,
	}
	require.NoError(t, runDocsSearch(context.Background(), &DocsSearchOptions{Query: "report", KBID: "kb1", Limit: 20, PageSize: docsPageSize, AllPages: true}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc))
	assert.Contains(t, out.String(), "d1")
}

// TestDocsSearch_PaginatesUntilTotal walks server-paginated results.
// Server-side filter has already been applied, so every returned item
// is in the result set; the runner just walks pages until total exhausted
// or --limit hit. With limit > total matches, we expect 2 pages.
func TestDocsSearch_PaginatesUntilTotal(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	page1 := make([]sdk.Knowledge, docsPageSize)
	for i := range page1 {
		page1[i] = sdk.Knowledge{ID: "p1", Title: "needle"}
	}
	page2 := []sdk.Knowledge{{ID: "found", Title: "needle here"}}
	svc := &fakeDocsSearchSvc{
		pages: map[int][]sdk.Knowledge{1: page1, 2: page2},
		total: int64(docsPageSize) + 1,
	}
	require.NoError(t, runDocsSearch(context.Background(), &DocsSearchOptions{Query: "needle", KBID: "kb1", Limit: docsPageSize + 1, PageSize: docsPageSize, AllPages: true}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc))
	assert.Contains(t, out.String(), "found")
	assert.Equal(t, []int{1, 2}, svc.calls, "must page past the first batch when more items reported")
}

func TestDocsSearch_StopsAtLimit(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	page1 := make([]sdk.Knowledge, 50)
	for i := range page1 {
		page1[i] = sdk.Knowledge{ID: "match", Title: "needle"}
	}
	svc := &fakeDocsSearchSvc{pages: map[int][]sdk.Knowledge{1: page1}, total: 1000}
	require.NoError(t, runDocsSearch(context.Background(), &DocsSearchOptions{Query: "needle", KBID: "kb1", Limit: 3, PageSize: docsPageSize, AllPages: true}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc))
	// Must not request page 2 because limit was hit mid-page.
	assert.Equal(t, []int{1}, svc.calls)
}

func TestDocsSearch_JSON(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeDocsSearchSvc{
		pages: map[int][]sdk.Knowledge{1: {{ID: "d1", Title: "match"}}},
		total: 1,
	}
	require.NoError(t, runDocsSearch(context.Background(), &DocsSearchOptions{Query: "match", KBID: "kb1", Limit: 20, PageSize: docsPageSize, AllPages: true}, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc))
	got := out.String()
	var env struct {
		OK   bool            `json:"ok"`
		Data []sdk.Knowledge `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(got), &env), "expected valid JSON envelope, got: %q", got)
	assert.True(t, env.OK, "envelope.ok must be true")
	assert.Contains(t, got, `"id":"d1"`)
}

func TestDocsSearch_NetworkError(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeDocsSearchSvc{err: errors.New("HTTP error 404: kb not found")}
	err := runDocsSearch(context.Background(), &DocsSearchOptions{Query: "x", KBID: "missing", Limit: 20, PageSize: docsPageSize, AllPages: true}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc)
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeResourceNotFound, typed.Code)
}

// TestSearchDocs_AllPagesFlag_DefaultsTrue_WalksAllPages locks in that the
// historic walk-all-pages behavior is preserved when the new --all-pages flag
// is left at its default (true). Three pages of fake data, all match the
// substring; the run must request every page.
func TestSearchDocs_AllPagesFlag_DefaultsTrue_WalksAllPages(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeDocsSearchSvc{
		pages: map[int][]sdk.Knowledge{
			1: {{ID: "d1", Title: "needle"}, {ID: "d2", Title: "needle"}},
			2: {{ID: "d3", Title: "needle"}},
			3: {},
		},
		total: 3,
	}
	opts := &DocsSearchOptions{Query: "needle", KBID: "kb_abc", Limit: 100, PageSize: 2, AllPages: true}
	require.NoError(t, runDocsSearch(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc))
	assert.GreaterOrEqual(t, len(svc.calls), 2, "must walk multi pages by default")
}

// TestSearchDocs_AllPagesFalse_StopsAtFirstPage asserts that --all-pages=false
// caps server round-trips at one, even when the server reports far more
// items available. New v0.5 opt-out for the walk-all default.
func TestSearchDocs_AllPagesFalse_StopsAtFirstPage(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeDocsSearchSvc{
		pages: map[int][]sdk.Knowledge{1: {{ID: "d1", Title: "needle"}, {ID: "d2", Title: "needle"}}},
		total: 100,
	}
	opts := &DocsSearchOptions{Query: "needle", KBID: "kb_abc", Limit: 100, PageSize: 2, AllPages: false}
	require.NoError(t, runDocsSearch(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc))
	assert.Len(t, svc.calls, 1, "must stop at first page when --all-pages=false")
}

// TestSearchDocs_KeywordPassedToFilter pins the v0.5 switch from client-side
// substring filtering to server-side ?keyword= via ListKnowledgeWithFilter.
// The query argument must arrive on the filter struct (not a discarded
// client-side variable).
func TestSearchDocs_KeywordPassedToFilter(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeDocsSearchSvc{pages: map[int][]sdk.Knowledge{1: {{ID: "d1"}}}, total: 1}
	require.NoError(t, runDocsSearch(context.Background(), &DocsSearchOptions{Query: "my-query", KBID: "kb1", Limit: 20, PageSize: docsPageSize, AllPages: true}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc))
	assert.Equal(t, "my-query", svc.lastFilter.Keyword, "Query must be threaded as filter.Keyword on ListKnowledgeWithFilter")
	// Other filter fields must be empty - search docs only forwards the keyword.
	assert.Empty(t, svc.lastFilter.ParseStatus)
	assert.Empty(t, svc.lastFilter.FileType)
	assert.Empty(t, svc.lastFilter.Source)
	assert.Empty(t, svc.lastFilter.TagID)
}

// TestSearchDocs_PageSizeBound asserts the 1..1000 range guard mirrors the
// session/doc list cap. Out-of-range values must produce
// input.invalid_argument and never reach the SDK.
func TestSearchDocs_PageSizeBound(t *testing.T) {
	for _, ps := range []int{0, -1, 1001} {
		err := runDocsSearch(context.Background(), &DocsSearchOptions{Query: "t", KBID: "k", Limit: 50, PageSize: ps}, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, &fakeDocsSearchSvc{})
		require.Error(t, err)
		var typed *cmdutil.Error
		require.ErrorAs(t, err, &typed)
		assert.Equal(t, cmdutil.CodeInputInvalidArgument, typed.Code, "page_size=%d", ps)
	}
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	require.NoError(t, err)
	return v
}

// TestDocsSearch_HasMore asserts the meta.has_more truncation signal: true when
// more matches than --limit exist (over-fetch detects it, data trimmed to
// --limit), absent/false when the full result set fits. Mirrors the list
// commands' contract so an agent can tell its search was capped.
func TestDocsSearch_HasMore(t *testing.T) {
	page := make([]sdk.Knowledge, 10)
	for i := range page {
		page[i] = sdk.Knowledge{ID: "match", Title: "needle"}
	}
	type meta struct {
		Count   int  `json:"count"`
		HasMore bool `json:"has_more"`
	}
	parse := func(t *testing.T, s string) meta {
		var env struct {
			Meta meta `json:"meta"`
		}
		require.NoError(t, json.Unmarshal([]byte(s), &env), "got %q", s)
		return env.Meta
	}

	t.Run("truncated -> has_more true, data trimmed", func(t *testing.T) {
		out, _ := iostreams.SetForTest(t)
		svc := &fakeDocsSearchSvc{pages: map[int][]sdk.Knowledge{1: page}, total: 10}
		require.NoError(t, runDocsSearch(context.Background(),
			&DocsSearchOptions{Query: "needle", KBID: "kb1", Limit: 3, PageSize: docsPageSize, AllPages: true},
			&cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc))
		m := parse(t, out.String())
		assert.Equal(t, 3, m.Count, "data must be trimmed to --limit")
		assert.True(t, m.HasMore, "has_more must be true when results exceed --limit")
	})

	t.Run("fits -> has_more false", func(t *testing.T) {
		out, _ := iostreams.SetForTest(t)
		svc := &fakeDocsSearchSvc{pages: map[int][]sdk.Knowledge{1: page[:2]}, total: 2}
		require.NoError(t, runDocsSearch(context.Background(),
			&DocsSearchOptions{Query: "needle", KBID: "kb1", Limit: 20, PageSize: docsPageSize, AllPages: true},
			&cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc))
		m := parse(t, out.String())
		assert.Equal(t, 2, m.Count)
		assert.False(t, m.HasMore, "has_more must be false/absent when results fit under --limit")
	})
}
