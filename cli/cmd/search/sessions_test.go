package search

import (
	"context"
	"errors"
	"testing"

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
	require.NoError(t, runSessionsSearch(context.Background(), &SessionsSearchOptions{Query: "design", Limit: 20}, svc))
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
	require.NoError(t, runSessionsSearch(context.Background(), &SessionsSearchOptions{Query: "missing", Limit: 20}, svc))
	assert.Contains(t, out.String(), "(no matches)")
}

func TestSessionsSearch_PaginatesAndStopsAtLimit(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	page1 := make([]sdk.Session, sessionsPageSize)
	for i := range page1 {
		page1[i] = sdk.Session{ID: "m", Title: "needle"}
	}
	svc := &fakeSessionsSearchSvc{pages: map[int][]sdk.Session{1: page1}, total: 1000}
	require.NoError(t, runSessionsSearch(context.Background(), &SessionsSearchOptions{Query: "needle", Limit: 5}, svc))
	assert.Equal(t, []int{1}, svc.calls, "stops paging when limit reached")
}

func TestSessionsSearch_NetworkError(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeSessionsSearchSvc{err: errors.New("HTTP error 500: internal")}
	err := runSessionsSearch(context.Background(), &SessionsSearchOptions{Query: "x", Limit: 20}, svc)
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeServerError, typed.Code)
}
