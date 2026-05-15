package chunkcmd

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

type listCall struct {
	docID    string
	page     int
	pageSize int
}

type fakeListSvc struct {
	calls   []listCall
	pages   [][]sdk.Chunk
	totals  []int64
	errs    []error
	callIdx int
}

func (f *fakeListSvc) ListKnowledgeChunks(_ context.Context, docID string, page, pageSize int) ([]sdk.Chunk, int64, error) {
	f.calls = append(f.calls, listCall{docID, page, pageSize})
	defer func() { f.callIdx++ }()
	if f.callIdx >= len(f.pages) {
		return nil, 0, nil
	}
	return f.pages[f.callIdx], f.totals[f.callIdx], f.errs[f.callIdx]
}

func TestList_Happy(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeListSvc{
		pages: [][]sdk.Chunk{{
			{ID: "c1", ChunkIndex: 0, Content: "hello", ChunkType: "text", IsEnabled: true},
			{ID: "c2", ChunkIndex: 1, Content: "world", ChunkType: "text", IsEnabled: true},
		}},
		totals: []int64{2}, errs: []error{nil},
	}
	opts := &ListOptions{DocID: "doc_abc", Limit: 50, PageSize: 50}
	require.NoError(t, runList(context.Background(), opts, &cmdutil.JSONOptions{}, svc))
	require.Len(t, svc.calls, 1)
	assert.Equal(t, "doc_abc", svc.calls[0].docID)
	assert.Equal(t, 1, svc.calls[0].page)
	assert.Equal(t, 50, svc.calls[0].pageSize)
	// JSON mode emits a bare array; both ids must be present.
	body := out.String()
	assert.Contains(t, body, `"c1"`)
	assert.Contains(t, body, `"c2"`)
}

// TestList_AllPages_StopsOnEmptyPage exercises the empty-page stop branch:
// total is large enough that page*pageSize < total never trips, so the loop
// can only terminate when the server returns an empty page.
func TestList_AllPages_StopsOnEmptyPage(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeListSvc{
		pages: [][]sdk.Chunk{
			{{ID: "c1"}, {ID: "c2"}},
			{{ID: "c3"}},
			{}, // empty page → done
		},
		// Inflated total isolates the empty-page branch from the
		// page*pageSize >= total branch.
		totals: []int64{100, 100, 100},
		errs:   []error{nil, nil, nil},
	}
	opts := &ListOptions{DocID: "doc_abc", AllPages: true, PageSize: 2, Limit: 1000}
	require.NoError(t, runList(context.Background(), opts, &cmdutil.JSONOptions{}, svc))
	assert.Equal(t, 3, len(svc.calls), "must stop on empty page, not loop forever")
}

// TestList_AllPages_StopsOnTotalExhausted exercises the page*pageSize >= total
// stop branch: server never returns an empty page in the requested window, so
// the loop must exit when accumulated coverage reaches total.
func TestList_AllPages_StopsOnTotalExhausted(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeListSvc{
		pages: [][]sdk.Chunk{
			{{ID: "c1"}, {ID: "c2"}},
			{{ID: "c3"}},
		},
		totals: []int64{3, 3},
		errs:   []error{nil, nil},
	}
	opts := &ListOptions{DocID: "doc_abc", AllPages: true, PageSize: 2, Limit: 1000}
	require.NoError(t, runList(context.Background(), opts, &cmdutil.JSONOptions{}, svc))
	// After page 2: page*pageSize=4 >= total=3 → stop. No 3rd request.
	assert.Equal(t, 2, len(svc.calls), "must stop when total exhausted, no extra empty probe")
}

// TestList_AllPages_LimitTruncatesAccumulated exercises the limit-cap stop
// branch: pagination halts as soon as accumulated >= limit, and the result
// is sliced to exactly --limit items regardless of how the last page over-ran.
func TestList_AllPages_LimitTruncatesAccumulated(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeListSvc{
		pages: [][]sdk.Chunk{
			{{ID: "c1"}, {ID: "c2"}},
			{{ID: "c3"}, {ID: "c4"}},
			{{ID: "c5"}}, // should not be requested — limit hits first
		},
		totals: []int64{5, 5, 5},
		errs:   []error{nil, nil, nil},
	}
	opts := &ListOptions{DocID: "doc_abc", AllPages: true, PageSize: 2, Limit: 3}
	require.NoError(t, runList(context.Background(), opts, &cmdutil.JSONOptions{}, svc))
	// After page 2: accum=4 >= limit=3 → stop. Third page never requested.
	assert.LessOrEqual(t, len(svc.calls), 2, "must not walk past limit-hit point")
	// Result must be exactly --limit items (server returned 4, sliced to 3).
	var got []sdk.Chunk
	require.NoError(t, json.Unmarshal(out.Bytes(), &got))
	assert.Len(t, got, 3, "accumulated must be sliced to exactly --limit")
	// IDs preserve order: first 3 from the first 2 pages.
	assert.Equal(t, []string{"c1", "c2", "c3"}, []string{got[0].ID, got[1].ID, got[2].ID})
}

func TestList_LimitInvalid(t *testing.T) {
	svc := &fakeListSvc{}
	for _, lim := range []int{0, -1, 1001} {
		err := runList(context.Background(), &ListOptions{DocID: "d", Limit: lim, PageSize: 50}, &cmdutil.JSONOptions{}, svc)
		require.Error(t, err, "expect error for --limit %d", lim)
		assert.Contains(t, err.Error(), "input.invalid_argument")
	}
}

func TestList_PageSizeInvalid(t *testing.T) {
	svc := &fakeListSvc{}
	for _, ps := range []int{0, -1, 1001} {
		err := runList(context.Background(), &ListOptions{DocID: "d", Limit: 50, PageSize: ps}, &cmdutil.JSONOptions{}, svc)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "input.invalid_argument")
	}
}

func TestList_MissingDoc_FlagError(t *testing.T) {
	cmd := NewCmdList(nil)
	cmd.SetArgs([]string{})
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	require.Error(t, cmd.Execute(), "expect required-flag error for missing --doc")
}

func TestList_Human_TableHeader(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeListSvc{
		pages: [][]sdk.Chunk{{
			{ID: "c1", ChunkIndex: 0, Content: "the quick brown fox", ChunkType: "text", IsEnabled: true, UpdatedAt: "2026-05-15T12:00:00Z"},
		}},
		totals: []int64{1}, errs: []error{nil},
	}
	require.NoError(t, runList(context.Background(), &ListOptions{DocID: "doc_abc", Limit: 50, PageSize: 50}, nil, svc))
	body := out.String()
	for _, want := range []string{"CHUNK_ID", "INDEX", "TYPE", "ENABLED", "PREVIEW", "UPDATED", "c1", "text"} {
		assert.Contains(t, body, want)
	}
}

func TestList_Human_PreviewTruncatedTo80(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	long := strings.Repeat("a", 200)
	svc := &fakeListSvc{
		pages:  [][]sdk.Chunk{{{ID: "c1", Content: long, ChunkType: "text", IsEnabled: true}}},
		totals: []int64{1}, errs: []error{nil},
	}
	require.NoError(t, runList(context.Background(), &ListOptions{DocID: "doc_abc", Limit: 50, PageSize: 50}, nil, svc))
	body := out.String()
	// 80-col preview means we never see the 100th `a` from the content (only column truncation kicks in).
	assert.NotContains(t, body, strings.Repeat("a", 100), "preview must be truncated to ~80 chars")
}

func TestList_JSON_BareArray(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeListSvc{
		pages: [][]sdk.Chunk{{
			{ID: "c1", KnowledgeID: "doc_abc", KnowledgeBaseID: "kb_x"},
		}},
		totals: []int64{1}, errs: []error{nil},
	}
	require.NoError(t, runList(context.Background(), &ListOptions{DocID: "doc_abc", Limit: 50, PageSize: 50}, &cmdutil.JSONOptions{}, svc))
	var got []sdk.Chunk
	require.NoError(t, json.Unmarshal(out.Bytes(), &got))
	require.Len(t, got, 1)
	assert.Equal(t, "doc_abc", got[0].KnowledgeID)
	assert.Equal(t, "kb_x", got[0].KnowledgeBaseID)
	// Bare SDK keys, not custom CLI envelope.
	assert.Contains(t, out.String(), `"knowledge_id":"doc_abc"`)
	assert.NotContains(t, out.String(), `"doc_id"`)
}

func TestList_EmptyResultRendersBareArray(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeListSvc{pages: [][]sdk.Chunk{{}}, totals: []int64{0}, errs: []error{nil}}
	require.NoError(t, runList(context.Background(), &ListOptions{DocID: "doc_abc", Limit: 50, PageSize: 50}, &cmdutil.JSONOptions{}, svc))
	assert.Equal(t, "[]\n", out.String())
}
