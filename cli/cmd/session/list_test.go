package sessioncmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

// fakeListService scripts a GetSessionsByTenant response.
type fakeListService struct {
	items       []sdk.Session
	total       int
	err         error
	gotPage     int
	gotPageSize int
}

func (f *fakeListService) GetSessionsByTenant(_ context.Context, page, pageSize int) ([]sdk.Session, int, error) {
	f.gotPage = page
	f.gotPageSize = pageSize
	return f.items, f.total, f.err
}

func TestList_Empty(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeListService{items: nil, total: 0}
	require.NoError(t, runList(context.Background(), &ListOptions{PageSize: 30}, nil, svc))
	assert.Contains(t, out.String(), "no sessions")
}

func TestList_Table(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeListService{
		items: []sdk.Session{
			{ID: "s_1", Title: "Design review", CreatedAt: "2026-05-10T09:00:00Z", UpdatedAt: "2026-05-12T14:00:00Z"},
			{ID: "s_2", Title: "RAG bug repro", CreatedAt: "2026-05-09T08:00:00Z", UpdatedAt: "2026-05-11T11:00:00Z"},
		},
		total: 2,
	}
	require.NoError(t, runList(context.Background(), &ListOptions{PageSize: 30}, nil, svc))
	got := out.String()
	assert.Contains(t, got, "s_1")
	assert.Contains(t, got, "Design review")
	assert.Contains(t, got, "s_2")
	assert.Equal(t, 1, svc.gotPage)
	assert.Equal(t, 30, svc.gotPageSize)
}

func TestList_JSON_BareArray(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeListService{
		items: []sdk.Session{
			{ID: "s_1", Title: "T1", UpdatedAt: "2026-05-12T14:00:00Z"},
		},
		total: 47,
	}
	require.NoError(t, runList(context.Background(), &ListOptions{PageSize: 10}, &cmdutil.JSONOptions{}, svc))

	// CLI always asks for page 1 of size --page-size; pagination is server-internal.
	assert.Equal(t, 1, svc.gotPage)
	assert.Equal(t, 10, svc.gotPageSize)
	body := out.String()
	assert.True(t, strings.HasPrefix(strings.TrimSpace(body), `[`), "bare array expected; got %q", body)
	assert.Contains(t, body, `"id":"s_1"`)
	assert.NotContains(t, body, `"ok":`)
	assert.NotContains(t, body, `"_meta":`)
	assert.NotContains(t, body, `"has_more":`)
	assert.NotContains(t, body, `"total":`)
}

func TestList_NilItems_RendersAsBareEmptyArray(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeListService{items: nil, total: 0}
	require.NoError(t, runList(context.Background(), &ListOptions{PageSize: 30}, &cmdutil.JSONOptions{}, svc))
	assert.Equal(t, "[]", strings.TrimSpace(out.String()))
}

func TestList_BadPagination(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	cases := []struct {
		size int
		name string
	}{
		{0, "size < 1"},
		{1001, "size > max"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := runList(context.Background(), &ListOptions{PageSize: tc.size}, nil, &fakeListService{})
			require.Error(t, err)
			var typed *cmdutil.Error
			require.ErrorAs(t, err, &typed)
			assert.Equal(t, cmdutil.CodeInputInvalidArgument, typed.Code)
		})
	}
}

func TestList_NetworkError_TypedCode(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeListService{err: errors.New("HTTP error 401: unauthenticated")}
	err := runList(context.Background(), &ListOptions{PageSize: 30}, nil, svc)
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeAuthUnauthenticated, typed.Code)
}

// Sanity: title with multi-rune content (CJK) should not crash truncation.
func TestList_NonASCIITitle(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeListService{items: []sdk.Session{{ID: "s_zh", Title: strings.Repeat("中文", 50)}}, total: 1}
	require.NoError(t, runList(context.Background(), &ListOptions{PageSize: 30}, nil, svc))
	assert.Contains(t, out.String(), "s_zh")
}

func TestList_SinceFilter_DropsOldSessions(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	now := time.Now()
	items := []sdk.Session{
		{ID: "recent", Title: "today", UpdatedAt: now.Add(-1 * time.Hour).Format(time.RFC3339)},
		{ID: "old", Title: "last month", UpdatedAt: now.Add(-30 * 24 * time.Hour).Format(time.RFC3339)},
		{ID: "yesterday", Title: "yday", UpdatedAt: now.Add(-23 * time.Hour).Format(time.RFC3339)},
	}
	require.NoError(t, runList(context.Background(),
		&ListOptions{PageSize: 30, Since: "7d"}, nil,
		&fakeListService{items: items, total: 3}))
	got := out.String()
	assert.Contains(t, got, "recent")
	assert.Contains(t, got, "yesterday")
	assert.NotContains(t, got, "old", "30-day-old session should be filtered out by --since 7d")
}

func TestList_SinceFilter_ParseDuration_Variants(t *testing.T) {
	cases := []string{"24h", "1h30m", "30m", "7d", "0.5d", "168h"}
	for _, v := range cases {
		t.Run(v, func(t *testing.T) {
			_, _ = iostreams.SetForTest(t)
			require.NoError(t, runList(context.Background(),
				&ListOptions{PageSize: 30, Since: v}, nil,
				&fakeListService{items: []sdk.Session{}, total: 0}),
				"--since=%q should parse", v)
		})
	}
}

func TestList_SinceFilter_RejectsInvalidDuration(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	err := runList(context.Background(),
		&ListOptions{PageSize: 30, Since: "bogus"}, nil,
		&fakeListService{items: []sdk.Session{}, total: 0})
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeInputInvalidArgument, typed.Code)
}

func TestList_SinceFilter_RejectsNonPositive(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	for _, v := range []string{"0d", "0h", "-1h"} {
		err := runList(context.Background(),
			&ListOptions{PageSize: 30, Since: v}, nil,
			&fakeListService{items: []sdk.Session{}, total: 0})
		require.Error(t, err, "--since=%q should reject", v)
		var typed *cmdutil.Error
		require.ErrorAs(t, err, &typed)
		assert.Equal(t, cmdutil.CodeInputInvalidArgument, typed.Code)
	}
}

// makeSessions builds N sessions with distinct IDs and descending UpdatedAt.
func makeSessions(n int) []sdk.Session {
	base := time.Now()
	out := make([]sdk.Session, n)
	for i := 0; i < n; i++ {
		out[i] = sdk.Session{
			ID:        fmt.Sprintf("s_%03d", i),
			Title:     fmt.Sprintf("title-%03d", i),
			UpdatedAt: base.Add(-time.Duration(i) * time.Minute).Format(time.RFC3339),
		}
	}
	return out
}

// pagedSessionSvc returns server-paginated session results from a flat slice
// and records page numbers requested.
type pagedSessionSvc struct {
	all      []sdk.Session
	calls    []int
	pageSize int
}

func (p *pagedSessionSvc) GetSessionsByTenant(_ context.Context, page, pageSize int) ([]sdk.Session, int, error) {
	p.calls = append(p.calls, page)
	p.pageSize = pageSize
	start := (page - 1) * pageSize
	if start >= len(p.all) {
		return []sdk.Session{}, len(p.all), nil
	}
	end := start + pageSize
	if end > len(p.all) {
		end = len(p.all)
	}
	return p.all[start:end], len(p.all), nil
}

func TestList_Limit_LessThanPageSize_SlicesToLimit(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeListService{items: makeSessions(20), total: 20}
	require.NoError(t, runList(context.Background(),
		&ListOptions{PageSize: 20, Limit: 5}, &cmdutil.JSONOptions{}, svc))
	got := strings.Count(out.String(), `"id":"s_`)
	assert.Equal(t, 5, got, "--limit 5 must slice 20 items down to 5")
}

func TestList_Limit_GreaterThanPageSize_NoCap(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeListService{items: makeSessions(10), total: 10}
	require.NoError(t, runList(context.Background(),
		&ListOptions{PageSize: 10, Limit: 50}, &cmdutil.JSONOptions{}, svc))
	got := strings.Count(out.String(), `"id":"s_`)
	assert.Equal(t, 10, got)
}

func TestList_Limit_Negative_Rejected(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	err := runList(context.Background(),
		&ListOptions{PageSize: 30, Limit: -1}, nil,
		&fakeListService{})
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeInputInvalidArgument, typed.Code)
}

func TestList_AllPages_WalksAllServerPages(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &pagedSessionSvc{all: makeSessions(45)}
	require.NoError(t, runList(context.Background(),
		&ListOptions{PageSize: 20, AllPages: true}, &cmdutil.JSONOptions{}, svc))
	assert.Equal(t, []int{1, 2, 3}, svc.calls)
	got := strings.Count(out.String(), `"id":"s_`)
	assert.Equal(t, 45, got)
}

func TestList_AllPages_WithLimit_StopsAtLimit(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &pagedSessionSvc{all: makeSessions(200)}
	require.NoError(t, runList(context.Background(),
		&ListOptions{PageSize: 20, AllPages: true, Limit: 50}, &cmdutil.JSONOptions{}, svc))
	got := strings.Count(out.String(), `"id":"s_`)
	assert.Equal(t, 50, got)
	assert.LessOrEqual(t, len(svc.calls), 3, "must not fetch beyond what fills --limit")
}

