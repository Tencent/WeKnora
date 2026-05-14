package sessioncmd

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/format"
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
	require.NoError(t, runList(context.Background(), &ListOptions{Page: 1, PageSize: 30}, nil, svc))
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
	require.NoError(t, runList(context.Background(), &ListOptions{Page: 1, PageSize: 30}, nil, svc))
	got := out.String()
	assert.Contains(t, got, "s_1")
	assert.Contains(t, got, "Design review")
	assert.Contains(t, got, "s_2")
	assert.Equal(t, 1, svc.gotPage)
	assert.Equal(t, 30, svc.gotPageSize)
}

func TestList_JSON_WithMeta(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeListService{
		items: []sdk.Session{
			{ID: "s_1", Title: "T1", UpdatedAt: "2026-05-12T14:00:00Z"},
		},
		total: 47,
	}
	require.NoError(t, runList(context.Background(), &ListOptions{Page: 2, PageSize: 10}, &cmdutil.JSONOptions{}, svc))

	var env format.Envelope
	require.NoError(t, json.Unmarshal(out.Bytes(), &env))
	require.True(t, env.OK)
	// Pagination flags are forwarded.
	assert.Equal(t, 2, svc.gotPage)
	assert.Equal(t, 10, svc.gotPageSize)
	// envelope.data.items shaped + paging metadata in _meta
	body := out.String()
	assert.Contains(t, body, `"id":"s_1"`)
	assert.Contains(t, body, `"items":`)
	// has_more inferred from page*pageSize < total (2*10=20 < 47).
	assert.Contains(t, body, `"has_more":true`)
}

func TestList_JSON_LastPage_NoHasMore(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeListService{
		items: []sdk.Session{{ID: "s_1"}},
		total: 11,
	}
	require.NoError(t, runList(context.Background(), &ListOptions{Page: 2, PageSize: 10}, &cmdutil.JSONOptions{}, svc))
	// page*size = 20 ≥ total 11 → has_more must be false (omitempty drops the key)
	body := out.String()
	assert.NotContains(t, body, `"has_more":true`)
}

func TestList_NilItems_RendersAsEmptyArray(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeListService{items: nil, total: 0}
	require.NoError(t, runList(context.Background(), &ListOptions{Page: 1, PageSize: 30}, &cmdutil.JSONOptions{}, svc))
	assert.Contains(t, out.String(), `"items":[]`)
}

func TestList_BadPagination(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	cases := []struct {
		page, size int
		name       string
	}{
		{0, 30, "page < 1"},
		{-1, 30, "page negative"},
		{1, 0, "size < 1"},
		{1, 1001, "size > max"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := runList(context.Background(), &ListOptions{Page: tc.page, PageSize: tc.size}, nil, &fakeListService{})
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
	err := runList(context.Background(), &ListOptions{Page: 1, PageSize: 30}, nil, svc)
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeAuthUnauthenticated, typed.Code)
}

// Sanity: title with multi-rune content (CJK) should not crash truncation.
func TestList_NonASCIITitle(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeListService{items: []sdk.Session{{ID: "s_zh", Title: strings.Repeat("中文", 50)}}, total: 1}
	require.NoError(t, runList(context.Background(), &ListOptions{Page: 1, PageSize: 30}, nil, svc))
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
		&ListOptions{Page: 1, PageSize: 30, Since: "7d"}, nil,
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
				&ListOptions{Page: 1, PageSize: 30, Since: v}, nil,
				&fakeListService{items: []sdk.Session{}, total: 0}),
				"--since=%q should parse", v)
		})
	}
}

func TestList_SinceFilter_RejectsInvalidDuration(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	err := runList(context.Background(),
		&ListOptions{Page: 1, PageSize: 30, Since: "bogus"}, nil,
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
			&ListOptions{Page: 1, PageSize: 30, Since: v}, nil,
			&fakeListService{items: []sdk.Session{}, total: 0})
		require.Error(t, err, "--since=%q should reject", v)
		var typed *cmdutil.Error
		require.ErrorAs(t, err, &typed)
		assert.Equal(t, cmdutil.CodeInputInvalidArgument, typed.Code)
	}
}
