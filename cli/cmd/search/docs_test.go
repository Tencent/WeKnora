package search

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

// fakeDocsSearchSvc scripts paginated ListKnowledge responses. Pages are
// indexed 1-based; items keyed by page.
type fakeDocsSearchSvc struct {
	pages map[int][]sdk.Knowledge
	total int64
	err   error
	calls []int // page numbers requested, for assertions
}

func (f *fakeDocsSearchSvc) ListKnowledge(_ context.Context, kbID string, page, pageSize int, tagID string) ([]sdk.Knowledge, int64, error) {
	f.calls = append(f.calls, page)
	if f.err != nil {
		return nil, 0, f.err
	}
	return f.pages[page], f.total, nil
}

func TestDocsSearch_Substring(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeDocsSearchSvc{
		pages: map[int][]sdk.Knowledge{
			1: {
				{ID: "d1", Title: "Q3 Forecast", FileName: "q3.pdf", UpdatedAt: mustTime(t, "2026-05-10T00:00:00Z")},
				{ID: "d2", Title: "Random Notes", FileName: "notes.md", UpdatedAt: mustTime(t, "2026-05-12T00:00:00Z")},
				{ID: "d3", Title: "Q3 retro", FileName: "retro.pdf", UpdatedAt: mustTime(t, "2026-05-11T00:00:00Z")},
			},
		},
		total: 3,
	}
	require.NoError(t, runDocsSearch(context.Background(), &DocsSearchOptions{Query: "q3", KBID: "kb1", Limit: 20, PageSize: docsPageSize, AllPages: true}, nil, svc))
	got := out.String()
	assert.Contains(t, got, "d1")
	assert.Contains(t, got, "d3")
	assert.NotContains(t, got, "d2") // "Random Notes" doesn't contain q3
}

func TestDocsSearch_MatchesFileName(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeDocsSearchSvc{
		pages: map[int][]sdk.Knowledge{1: {{ID: "d1", Title: "Untitled", FileName: "report.pdf"}}},
		total: 1,
	}
	require.NoError(t, runDocsSearch(context.Background(), &DocsSearchOptions{Query: "report", KBID: "kb1", Limit: 20, PageSize: docsPageSize, AllPages: true}, nil, svc))
	assert.Contains(t, out.String(), "d1")
}

func TestDocsSearch_PaginatesUntilTotal(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	page1 := make([]sdk.Knowledge, docsPageSize)
	for i := range page1 {
		page1[i] = sdk.Knowledge{ID: "p1", Title: "no match"}
	}
	page2 := []sdk.Knowledge{{ID: "found", Title: "needle here"}}
	svc := &fakeDocsSearchSvc{
		pages: map[int][]sdk.Knowledge{1: page1, 2: page2},
		total: int64(docsPageSize) + 1,
	}
	require.NoError(t, runDocsSearch(context.Background(), &DocsSearchOptions{Query: "needle", KBID: "kb1", Limit: 20, PageSize: docsPageSize, AllPages: true}, nil, svc))
	assert.Contains(t, out.String(), "found")
	assert.Equal(t, []int{1, 2}, svc.calls, "must page past the first batch when no match on page 1")
}

func TestDocsSearch_StopsAtLimit(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	page1 := make([]sdk.Knowledge, 50)
	for i := range page1 {
		page1[i] = sdk.Knowledge{ID: "match", Title: "needle"}
	}
	svc := &fakeDocsSearchSvc{pages: map[int][]sdk.Knowledge{1: page1}, total: 1000}
	require.NoError(t, runDocsSearch(context.Background(), &DocsSearchOptions{Query: "needle", KBID: "kb1", Limit: 3, PageSize: docsPageSize, AllPages: true}, nil, svc))
	// Must not request page 2 because limit was hit mid-page.
	assert.Equal(t, []int{1}, svc.calls)
}

func TestDocsSearch_JSON(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeDocsSearchSvc{
		pages: map[int][]sdk.Knowledge{1: {{ID: "d1", Title: "match"}}},
		total: 1,
	}
	require.NoError(t, runDocsSearch(context.Background(), &DocsSearchOptions{Query: "match", KBID: "kb1", Limit: 20, PageSize: docsPageSize, AllPages: true}, &cmdutil.JSONOptions{}, svc))
	got := out.String()
	assert.True(t, strings.HasPrefix(strings.TrimSpace(got), "["), "expected bare JSON array, got: %q", got)
	assert.Contains(t, got, `"id":"d1"`)
	assert.NotContains(t, got, `"ok":`)
}

func TestDocsSearch_NetworkError(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeDocsSearchSvc{err: errors.New("HTTP error 404: kb not found")}
	err := runDocsSearch(context.Background(), &DocsSearchOptions{Query: "x", KBID: "missing", Limit: 20, PageSize: docsPageSize, AllPages: true}, nil, svc)
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
	require.NoError(t, runDocsSearch(context.Background(), opts, &cmdutil.JSONOptions{}, svc))
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
	require.NoError(t, runDocsSearch(context.Background(), opts, &cmdutil.JSONOptions{}, svc))
	assert.Len(t, svc.calls, 1, "must stop at first page when --all-pages=false")
}

// TestSearchDocs_PageSizeBound asserts the 1..1000 range guard mirrors the
// session/doc list canon. Out-of-range values must produce
// input.invalid_argument and never reach the SDK.
func TestSearchDocs_PageSizeBound(t *testing.T) {
	for _, ps := range []int{0, -1, 1001} {
		err := runDocsSearch(context.Background(), &DocsSearchOptions{Query: "t", KBID: "k", Limit: 50, PageSize: ps}, &cmdutil.JSONOptions{}, &fakeDocsSearchSvc{})
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
