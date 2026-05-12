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
	"github.com/Tencent/WeKnora/cli/internal/format"
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
	require.NoError(t, runDocsSearch(context.Background(), &DocsSearchOptions{Query: "q3", KBID: "kb1", Limit: 20}, svc))
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
	require.NoError(t, runDocsSearch(context.Background(), &DocsSearchOptions{Query: "report", KBID: "kb1", Limit: 20}, svc))
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
	require.NoError(t, runDocsSearch(context.Background(), &DocsSearchOptions{Query: "needle", KBID: "kb1", Limit: 20}, svc))
	assert.Contains(t, out.String(), "found")
	assert.Equal(t, []int{1, 2}, svc.calls, "must page past the first batch when no match on page 1")
}

func TestDocsSearch_StopsAtTopK(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	page1 := make([]sdk.Knowledge, 50)
	for i := range page1 {
		page1[i] = sdk.Knowledge{ID: "match", Title: "needle"}
	}
	svc := &fakeDocsSearchSvc{pages: map[int][]sdk.Knowledge{1: page1}, total: 1000}
	require.NoError(t, runDocsSearch(context.Background(), &DocsSearchOptions{Query: "needle", KBID: "kb1", Limit: 3}, svc))
	// Must not request page 2 because top-k was hit mid-page.
	assert.Equal(t, []int{1}, svc.calls)
}

func TestDocsSearch_JSON(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeDocsSearchSvc{
		pages: map[int][]sdk.Knowledge{1: {{ID: "d1", Title: "match"}}},
		total: 1,
	}
	require.NoError(t, runDocsSearch(context.Background(), &DocsSearchOptions{Query: "match", KBID: "kb1", Limit: 20, JSONOut: true}, svc))
	var env format.Envelope
	require.NoError(t, json.Unmarshal(out.Bytes(), &env))
	require.True(t, env.OK)
	assert.Contains(t, out.String(), `"id":"d1"`)
}

func TestDocsSearch_NetworkError(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeDocsSearchSvc{err: errors.New("HTTP error 404: kb not found")}
	err := runDocsSearch(context.Background(), &DocsSearchOptions{Query: "x", KBID: "missing", Limit: 20}, svc)
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeResourceNotFound, typed.Code)
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	require.NoError(t, err)
	return v
}
