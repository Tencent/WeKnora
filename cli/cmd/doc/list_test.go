package doc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/config"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

// fakeListSvc captures the request args and returns canned responses.
type fakeListSvc struct {
	items []sdk.Knowledge
	total int64
	err   error
	got   struct {
		kbID     string
		page     int
		pageSize int
		filter   sdk.KnowledgeListFilter
	}
}

func (f *fakeListSvc) ListKnowledgeWithFilter(_ context.Context, kbID string, page, pageSize int, filter sdk.KnowledgeListFilter) ([]sdk.Knowledge, int64, error) {
	f.got.kbID, f.got.page, f.got.pageSize, f.got.filter = kbID, page, pageSize, filter
	return f.items, f.total, f.err
}

// chdirIsolated parks cwd in a fresh tempdir so Factory.ResolveKB doesn't pick
// up a stray .weknora/project.yaml from the repo. Also clears WEKNORA_KB_ID
// for the duration of the test.
func chdirIsolated(t *testing.T) {
	t.Helper()
	prev, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(t.TempDir()))
	t.Cleanup(func() { _ = os.Chdir(prev) })
	t.Setenv("WEKNORA_KB_ID", "")
}

func TestList_Success_Human(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	now := time.Now()
	items := []sdk.Knowledge{
		{ID: "doc1", FileName: "alpha.pdf", FileSize: 2048, ParseStatus: "completed", UpdatedAt: now.Add(-1 * time.Hour)},
		{ID: "doc2", FileName: "beta.md", FileSize: 0, ParseStatus: "pending", UpdatedAt: now.Add(-2 * 24 * time.Hour)},
	}
	svc := &fakeListSvc{items: items, total: 2}
	opts := &ListOptions{PageSize: 20}
	require.NoError(t, runList(context.Background(), opts, nil, svc, "kb_xxx"))

	assert.Equal(t, "kb_xxx", svc.got.kbID)
	assert.Equal(t, 1, svc.got.page)
	assert.Equal(t, 20, svc.got.pageSize)
	assert.Equal(t, sdk.KnowledgeListFilter{}, svc.got.filter, "no flags ⇒ empty filter")

	got := out.String()
	for _, want := range []string{"ID", "NAME", "STATUS", "SIZE", "UPDATED", "doc1", "alpha.pdf", "completed", "2.0KB", "doc2", "beta.md", "pending"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q in:\n%s", want, got)
		}
	}
}

func TestList_Success_JSON(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeListSvc{items: []sdk.Knowledge{{ID: "doc1", FileName: "a.pdf"}}, total: 1}
	opts := &ListOptions{PageSize: 20}
	require.NoError(t, runList(context.Background(), opts, &cmdutil.JSONOptions{}, svc, "kb_xxx"))

	got := out.String()
	assert.True(t, strings.HasPrefix(strings.TrimSpace(got), `[`), "expected bare JSON array, got %q", got)
	assert.Contains(t, got, `"id":"doc1"`)
	assert.NotContains(t, got, `"ok":`)
	assert.NotContains(t, got, `"_meta":`)
}

func TestList_Empty_Human(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeListSvc{items: nil, total: 0}
	opts := &ListOptions{PageSize: 20}
	require.NoError(t, runList(context.Background(), opts, nil, svc, "kb_xxx"))
	assert.Contains(t, out.String(), "(no documents)")
}

func TestList_Empty_JSON(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeListSvc{items: nil, total: 0}
	opts := &ListOptions{PageSize: 20}
	require.NoError(t, runList(context.Background(), opts, &cmdutil.JSONOptions{}, svc, "kb_xxx"))

	got := strings.TrimSpace(out.String())
	assert.Equal(t, "[]", got, "empty list must serialize as bare `[]` not null")
}

func TestList_HTTPError_500(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeListSvc{err: errors.New("HTTP error 500: internal")}
	opts := &ListOptions{PageSize: 20}
	err := runList(context.Background(), opts, nil, svc, "kb_xxx")
	require.Error(t, err)

	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeServerError, typed.Code)
}

// TestList_KBIDRequired drives the cobra layer to verify Factory.ResolveKB's
// "no source supplied" path bubbles up as CodeKBIDRequired. Isolates cwd so
// no project.yaml sneaks in, and clears WEKNORA_KB_ID.
func TestList_KBIDRequired(t *testing.T) {
	chdirIsolated(t)
	_, _ = iostreams.SetForTest(t)

	cfg := &config.Config{
		CurrentContext: "default",
		Contexts:       map[string]config.Context{"default": {Host: "https://example"}},
	}
	f := &cmdutil.Factory{
		Config: func() (*config.Config, error) { return cfg, nil },
		Client: func() (*sdk.Client, error) {
			return nil, errors.New("client should not be called when kb id is missing")
		},
	}
	cmd := NewCmdList(f)
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{}) // no --kb
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	err := cmd.Execute()
	require.Error(t, err)

	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeKBIDRequired, typed.Code)
}

// TestList_KBFlagWiredToResolveKB confirms that --kb=kb_<id> passed at the
// cobra layer reaches Factory.ResolveKB and short-circuits without listing.
func TestList_KBFlagWiredToResolveKB(t *testing.T) {
	chdirIsolated(t)
	_, _ = iostreams.SetForTest(t)

	cfg := &config.Config{
		CurrentContext: "default",
		Contexts:       map[string]config.Context{"default": {Host: "https://example"}},
	}
	f := &cmdutil.Factory{
		Config: func() (*config.Config, error) { return cfg, nil },
		Client: func() (*sdk.Client, error) {
			return nil, errors.New("forced-after-resolvekb")
		},
	}
	// With --kb=kb_<id> supplied, ResolveKB short-circuits on the prefix
	// match without consulting the client. The RunE then asks for the client
	// to run the actual list - that call triggers the forced error.
	// Surfacing "forced-after-resolvekb" (rather than CodeKBIDRequired) is
	// the proof point that --kb was honored.
	cmd := NewCmdList(f)
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{"--kb", "kb_explicit"})
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	err := cmd.Execute()
	require.Error(t, err, "expected client construction error")
	assert.Contains(t, err.Error(), "forced-after-resolvekb",
		"should surface the Client closure error, not a kb-required error")
}

// pinning the sort order: most-recent-first regardless of input order.
func TestList_SortByUpdatedDesc(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	now := time.Now()
	// Server returns oldest first; CLI must reorder.
	items := []sdk.Knowledge{
		{ID: "old", FileName: "old.pdf", UpdatedAt: now.Add(-10 * 24 * time.Hour)},
		{ID: "new", FileName: "new.pdf", UpdatedAt: now.Add(-1 * time.Hour)},
	}
	svc := &fakeListSvc{items: items, total: 2}
	require.NoError(t, runList(context.Background(), &ListOptions{PageSize: 20}, nil, svc, "kb_xxx"))

	got := out.String()
	newIdx := strings.Index(got, "new.pdf")
	oldIdx := strings.Index(got, "old.pdf")
	require.GreaterOrEqual(t, newIdx, 0)
	require.GreaterOrEqual(t, oldIdx, 0)
	assert.Less(t, newIdx, oldIdx, "most-recent should render first")
}

// TestFormatSize sanity-checks the byte-count formatter without exporting it.
func TestFormatSize(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "-"},
		{-1, "-"},
		{900, "900B"},
		{2048, "2.0KB"},
		{5 * 1024 * 1024, "5.0MB"},
	}
	for _, c := range cases {
		if got := formatSize(c.in); got != c.want {
			t.Errorf("formatSize(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestList_StatusFilter_ForwardedToSDK(t *testing.T) {
	chdirIsolated(t)
	_, _ = iostreams.SetForTest(t)
	svc := &fakeListSvc{}
	opts := &ListOptions{PageSize: 20, Status: "failed"}
	require.NoError(t, runList(context.Background(), opts, nil, svc, "kb_xxx"))
	assert.Equal(t, "failed", svc.got.filter.ParseStatus,
		"--status must be forwarded as filter.ParseStatus for server-side filtering")
}

func TestList_StatusFilter_RejectsUnknownValue(t *testing.T) {
	chdirIsolated(t)
	_, _ = iostreams.SetForTest(t)
	svc := &fakeListSvc{}
	opts := &ListOptions{PageSize: 20, Status: "bogus"}
	err := runList(context.Background(), opts, nil, svc, "kb_xxx")
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeInputInvalidArgument, typed.Code)
	assert.Contains(t, typed.Message, "pending")
	assert.Contains(t, typed.Message, "failed")
}

func TestList_StatusFilter_AcceptsAllEnumValues(t *testing.T) {
	chdirIsolated(t)
	for _, v := range docListStatusValues {
		_, _ = iostreams.SetForTest(t)
		svc := &fakeListSvc{}
		opts := &ListOptions{PageSize: 20, Status: v}
		require.NoError(t, runList(context.Background(), opts, nil, svc, "kb_xxx"),
			"status=%q should be accepted", v)
	}
}

// makeDocs returns N Knowledge records with distinct IDs and descending
// UpdatedAt timestamps, useful for limit / pagination tests.
func makeDocs(n int) []sdk.Knowledge {
	base := time.Now()
	out := make([]sdk.Knowledge, n)
	for i := 0; i < n; i++ {
		out[i] = sdk.Knowledge{
			ID:        fmt.Sprintf("doc_%02d", i),
			FileName:  fmt.Sprintf("f_%02d.pdf", i),
			UpdatedAt: base.Add(-time.Duration(i) * time.Hour),
		}
	}
	return out
}

// pagedDocSvc returns server-paginated Knowledge results from a flat slice.
// Records the page numbers requested for assertion.
type pagedDocSvc struct {
	all      []sdk.Knowledge
	calls    []int // 1-based page numbers received
	pageSize int
}

func (p *pagedDocSvc) ListKnowledgeWithFilter(_ context.Context, _ string, page, pageSize int, _ sdk.KnowledgeListFilter) ([]sdk.Knowledge, int64, error) {
	p.calls = append(p.calls, page)
	p.pageSize = pageSize
	start := (page - 1) * pageSize
	if start >= len(p.all) {
		return []sdk.Knowledge{}, int64(len(p.all)), nil
	}
	end := start + pageSize
	if end > len(p.all) {
		end = len(p.all)
	}
	return p.all[start:end], int64(len(p.all)), nil
}

func TestList_Limit_LessThanPageSize_SlicesToLimit(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeListSvc{items: makeDocs(20), total: 20}
	opts := &ListOptions{PageSize: 20, Limit: 5}
	require.NoError(t, runList(context.Background(), opts, &cmdutil.JSONOptions{}, svc, "kb_xxx"))
	body := out.String()
	// Count occurrences of "id":"doc_" - should be exactly 5.
	got := strings.Count(body, `"id":"doc_`)
	assert.Equal(t, 5, got, "--limit 5 must slice 20 returned items to 5; body=\n%s", body)
}

func TestList_Limit_GreaterThanPageSize_NoCap(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeListSvc{items: makeDocs(10), total: 10}
	opts := &ListOptions{PageSize: 10, Limit: 50}
	require.NoError(t, runList(context.Background(), opts, &cmdutil.JSONOptions{}, svc, "kb_xxx"))
	got := strings.Count(out.String(), `"id":"doc_`)
	assert.Equal(t, 10, got, "--limit 50 with page-size 10 + 10 items returns all 10")
}

func TestList_Limit_Negative_Rejected(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	opts := &ListOptions{PageSize: 20, Limit: -1}
	err := runList(context.Background(), opts, nil, &fakeListSvc{}, "kb_xxx")
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeInputInvalidArgument, typed.Code)
}

func TestList_AllPages_WalksAllServerPages(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &pagedDocSvc{all: makeDocs(45)}
	opts := &ListOptions{PageSize: 20, AllPages: true}
	require.NoError(t, runList(context.Background(), opts, &cmdutil.JSONOptions{}, svc, "kb_xxx"))
	// 45 items / page_size 20 = 3 pages: 20 + 20 + 5.
	assert.Equal(t, []int{1, 2, 3}, svc.calls)
	got := strings.Count(out.String(), `"id":"doc_`)
	assert.Equal(t, 45, got)
}

func TestList_AllPages_WithLimit_StopsAtLimit(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &pagedDocSvc{all: makeDocs(200)}
	opts := &ListOptions{PageSize: 20, AllPages: true, Limit: 50}
	require.NoError(t, runList(context.Background(), opts, &cmdutil.JSONOptions{}, svc, "kb_xxx"))
	got := strings.Count(out.String(), `"id":"doc_`)
	assert.Equal(t, 50, got, "--limit 50 with --all-pages should stop after 50 items")
	// Should have called pages 1..3 (60 items) then capped at 50.
	assert.LessOrEqual(t, len(svc.calls), 3, "should not walk past the page that fills --limit")
}
