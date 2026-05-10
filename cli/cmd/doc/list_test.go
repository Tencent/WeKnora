package doc

import (
	"context"
	"errors"
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
		tagID    string
	}
}

func (f *fakeListSvc) ListKnowledge(_ context.Context, kbID string, page, pageSize int, tagID string) ([]sdk.Knowledge, int64, error) {
	f.got.kbID, f.got.page, f.got.pageSize, f.got.tagID = kbID, page, pageSize, tagID
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
	opts := &ListOptions{Page: 1, PageSize: 20}
	require.NoError(t, runList(context.Background(), opts, svc, "kb_xxx"))

	assert.Equal(t, "kb_xxx", svc.got.kbID)
	assert.Equal(t, 1, svc.got.page)
	assert.Equal(t, 20, svc.got.pageSize)
	assert.Equal(t, "", svc.got.tagID, "doc list does not filter by tag")

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
	opts := &ListOptions{Page: 1, PageSize: 20, JSONOut: true}
	require.NoError(t, runList(context.Background(), opts, svc, "kb_xxx"))

	got := out.String()
	assert.True(t, strings.HasPrefix(got, `{"ok":true`), "envelope should start with ok:true; got %q", got)
	assert.Contains(t, got, `"items":[`)
	assert.Contains(t, got, `"id":"doc1"`)
	assert.Contains(t, got, `"page":1`)
	assert.Contains(t, got, `"page_size":20`)
	assert.Contains(t, got, `"total":1`)
	assert.Contains(t, got, `"kb_id":"kb_xxx"`)
}

func TestList_Empty_Human(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeListSvc{items: nil, total: 0}
	opts := &ListOptions{Page: 1, PageSize: 20}
	require.NoError(t, runList(context.Background(), opts, svc, "kb_xxx"))
	assert.Contains(t, out.String(), "(no documents)")
}

func TestList_Empty_JSON(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeListSvc{items: nil, total: 0}
	opts := &ListOptions{Page: 1, PageSize: 20, JSONOut: true}
	require.NoError(t, runList(context.Background(), opts, svc, "kb_xxx"))

	got := out.String()
	assert.Contains(t, got, `"items":[]`, "items must serialize as [] not null")
	assert.NotContains(t, got, `"items":null`)
}

func TestList_HTTPError_500(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeListSvc{err: errors.New("HTTP error 500: internal")}
	opts := &ListOptions{Page: 1, PageSize: 20}
	err := runList(context.Background(), opts, svc, "kb_xxx")
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
		Client: func() (*sdk.Client, error) { return nil, errors.New("client should not be called when kb id is missing") },
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
	// to run the actual list — that call triggers the forced error.
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
	require.NoError(t, runList(context.Background(), &ListOptions{Page: 1, PageSize: 20}, svc, "kb_xxx"))

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

