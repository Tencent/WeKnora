package search

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

type fakeSessionsSearchSvc struct {
	pages map[int][]sdk.Session
	total int
	err   error
	calls []int
}

func (f *fakeSessionsSearchSvc) GetSessionsByTenant(_ context.Context, page, pageSize int) ([]sdk.Session, int, error) {
	f.calls = append(f.calls, page)
	if f.err != nil {
		return nil, 0, f.err
	}
	return f.pages[page], f.total, nil
}

func TestSessionsSearch_TitleAndDescription(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeSessionsSearchSvc{
		pages: map[int][]sdk.Session{1: {
			{ID: "s1", Title: "Design review", UpdatedAt: "2026-05-12"},
			{ID: "s2", Title: "Random", Description: "with design notes", UpdatedAt: "2026-05-11"},
			{ID: "s3", Title: "Marketing", UpdatedAt: "2026-05-10"},
		}},
		total: 3,
	}
	require.NoError(t, runSessionsSearch(context.Background(), &SessionsSearchOptions{Query: "design", Limit: 20, PageSize: sessionsPageSize, AllPages: true}, &cmdutil.FormatOptions{Mode: cmdutil.FormatHuman}, svc))
	got := out.String()
	assert.Contains(t, got, "s1")
	assert.Contains(t, got, "s2")
	assert.NotContains(t, got, "s3")
}

func TestSessionsSearch_NoMatches(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeSessionsSearchSvc{
		pages: map[int][]sdk.Session{1: {{Title: "foo"}}},
		total: 1,
	}
	require.NoError(t, runSessionsSearch(context.Background(), &SessionsSearchOptions{Query: "missing", Limit: 20, PageSize: sessionsPageSize, AllPages: true}, &cmdutil.FormatOptions{Mode: cmdutil.FormatHuman}, svc))
	assert.Contains(t, out.String(), "(no matches)")
}

func TestSessionsSearch_PaginatesAndStopsAtLimit(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	page1 := make([]sdk.Session, sessionsPageSize)
	for i := range page1 {
		page1[i] = sdk.Session{ID: "m", Title: "needle"}
	}
	svc := &fakeSessionsSearchSvc{pages: map[int][]sdk.Session{1: page1}, total: 1000}
	require.NoError(t, runSessionsSearch(context.Background(), &SessionsSearchOptions{Query: "needle", Limit: 5, PageSize: sessionsPageSize, AllPages: true}, &cmdutil.FormatOptions{Mode: cmdutil.FormatHuman}, svc))
	assert.Equal(t, []int{1}, svc.calls, "stops paging when limit reached")
}

func TestSessionsSearch_NetworkError(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeSessionsSearchSvc{err: errors.New("HTTP error 500: internal")}
	err := runSessionsSearch(context.Background(), &SessionsSearchOptions{Query: "x", Limit: 20, PageSize: sessionsPageSize, AllPages: true}, &cmdutil.FormatOptions{Mode: cmdutil.FormatHuman}, svc)
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeServerError, typed.Code)
}

// TestSessionsSearch_RendersFuzzyTime is a regression guard for the v0.5
// audit bug: `search sessions` printed UpdatedAt as the raw RFC3339 string
// while `session list` ran it through text.FuzzyAgoStr — same SDK field,
// two human renderings. Asserts the human output now renders relative time
// (and does NOT contain the RFC3339 "T" date/time separator).
func TestSessionsSearch_RendersFuzzyTime(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeSessionsSearchSvc{
		pages: map[int][]sdk.Session{1: {
			{ID: "s1", Title: "needle", UpdatedAt: time.Now().Add(-2 * time.Hour).Format(time.RFC3339)},
		}},
		total: 1,
	}
	require.NoError(t, runSessionsSearch(context.Background(), &SessionsSearchOptions{Query: "needle", Limit: 10, PageSize: sessionsPageSize, AllPages: true}, &cmdutil.FormatOptions{Mode: cmdutil.FormatHuman}, svc))
	body := out.String()
	assert.Contains(t, body, "hour", "must render relative time (e.g. 'about 2 hours ago'), not raw RFC3339")
	assert.NotContains(t, body, "T0", "raw RFC3339 has 'T' between date and time; fuzzyTime output should not")
}

// TestSearchSessions_AllPagesFlag_DefaultsTrue_WalksAllPages locks in that
// the historic walk-all-pages behavior is preserved when --all-pages is left
// at its default (true). Three pages of fake data with matches on each;
// the run must request more than one page.
func TestSearchSessions_AllPagesFlag_DefaultsTrue_WalksAllPages(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeSessionsSearchSvc{
		pages: map[int][]sdk.Session{
			1: {{ID: "s1", Title: "needle"}, {ID: "s2", Title: "needle"}},
			2: {{ID: "s3", Title: "needle"}},
			3: {},
		},
		total: 3,
	}
	opts := &SessionsSearchOptions{Query: "needle", Limit: 100, PageSize: 2, AllPages: true}
	require.NoError(t, runSessionsSearch(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc))
	assert.GreaterOrEqual(t, len(svc.calls), 2, "must walk multi pages by default")
}

// TestSearchSessions_AllPagesFalse_StopsAtFirstPage asserts that
// --all-pages=false caps server round-trips at one even when the server
// reports far more items available. New v0.5 opt-out for the walk-all default.
func TestSearchSessions_AllPagesFalse_StopsAtFirstPage(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeSessionsSearchSvc{
		pages: map[int][]sdk.Session{1: {{ID: "s1", Title: "needle"}, {ID: "s2", Title: "needle"}}},
		total: 100,
	}
	opts := &SessionsSearchOptions{Query: "needle", Limit: 100, PageSize: 2, AllPages: false}
	require.NoError(t, runSessionsSearch(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc))
	assert.Len(t, svc.calls, 1, "must stop at first page when --all-pages=false")
}

// TestSearchSessions_PageSizeBound asserts the 1..1000 range guard mirrors
// the session/doc list cap. Out-of-range values must produce
// input.invalid_argument and never reach the SDK.
func TestSearchSessions_PageSizeBound(t *testing.T) {
	for _, ps := range []int{0, -1, 1001} {
		err := runSessionsSearch(context.Background(), &SessionsSearchOptions{Query: "t", Limit: 50, PageSize: ps}, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, &fakeSessionsSearchSvc{})
		require.Error(t, err)
		var typed *cmdutil.Error
		require.ErrorAs(t, err, &typed)
		assert.Equal(t, cmdutil.CodeInputInvalidArgument, typed.Code, "page_size=%d", ps)
	}
}
