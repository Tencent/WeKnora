package search

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/format"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

type fakeKBSearchSvc struct {
	items []sdk.KnowledgeBase
	err   error
}

func (f *fakeKBSearchSvc) ListKnowledgeBases(_ context.Context) ([]sdk.KnowledgeBase, error) {
	return f.items, f.err
}

func TestKBSearch_Substring(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeKBSearchSvc{items: []sdk.KnowledgeBase{
		{ID: "kb1", Name: "Marketing Q3", KnowledgeCount: 10},
		{ID: "kb2", Name: "Engineering Docs", KnowledgeCount: 50},
		{ID: "kb3", Name: "Marketing Q4 Plan", KnowledgeCount: 5},
	}}
	require.NoError(t, runKBSearch(context.Background(), &KBSearchOptions{Query: "marketing", Limit: 20}, svc))
	got := out.String()
	assert.Contains(t, got, "kb1")
	assert.Contains(t, got, "kb3")
	assert.NotContains(t, got, "Engineering")
}

func TestKBSearch_CaseInsensitive(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeKBSearchSvc{items: []sdk.KnowledgeBase{
		{ID: "kb1", Name: "ENGINEERING"},
	}}
	require.NoError(t, runKBSearch(context.Background(), &KBSearchOptions{Query: "engineering", Limit: 20}, svc))
	assert.Contains(t, out.String(), "kb1")
}

func TestKBSearch_MatchesDescription(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeKBSearchSvc{items: []sdk.KnowledgeBase{
		{ID: "kb1", Name: "Engineering", Description: "all marketing docs are here"},
	}}
	require.NoError(t, runKBSearch(context.Background(), &KBSearchOptions{Query: "marketing", Limit: 20}, svc))
	assert.Contains(t, out.String(), "kb1")
}

func TestKBSearch_SortByNameLength(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeKBSearchSvc{items: []sdk.KnowledgeBase{
		{ID: "kb_long", Name: "very long name marketing"},
		{ID: "kb_short", Name: "marketing"},
		{ID: "kb_mid", Name: "marketing 2024"},
	}}
	require.NoError(t, runKBSearch(context.Background(), &KBSearchOptions{Query: "marketing", Limit: 20}, svc))
	got := out.String()
	// Order: shortest name first.
	iShort := strings.Index(got,"kb_short")
	iMid := strings.Index(got,"kb_mid")
	iLong := strings.Index(got,"kb_long")
	assert.Less(t, iShort, iMid)
	assert.Less(t, iMid, iLong)
}

func TestKBSearch_TopKHardCap(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeKBSearchSvc{items: []sdk.KnowledgeBase{
		{ID: "a", Name: "match-a"}, {ID: "b", Name: "match-b"},
		{ID: "c", Name: "match-c"}, {ID: "d", Name: "match-d"},
	}}
	require.NoError(t, runKBSearch(context.Background(), &KBSearchOptions{Query: "match", Limit: 2}, svc))
	got := out.String()
	count := 0
	for _, id := range []string{"a", "b", "c", "d"} {
		if strings.Contains(got, "match-"+id) {
			count++
		}
	}
	assert.Equal(t, 2, count)
}

func TestKBSearch_NoMatches(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeKBSearchSvc{items: []sdk.KnowledgeBase{{Name: "foo"}}}
	require.NoError(t, runKBSearch(context.Background(), &KBSearchOptions{Query: "bar", Limit: 20}, svc))
	assert.Contains(t, out.String(), "(no matches)")
}

func TestKBSearch_JSON(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeKBSearchSvc{items: []sdk.KnowledgeBase{{ID: "kb1", Name: "marketing"}}}
	require.NoError(t, runKBSearch(context.Background(), &KBSearchOptions{Query: "marketing", Limit: 20, JSONOut: true}, svc))

	var env format.Envelope
	require.NoError(t, json.Unmarshal(out.Bytes(), &env))
	require.True(t, env.OK)
	assert.Contains(t, out.String(), `"id":"kb1"`)
}

func TestKBSearch_NetworkError(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeKBSearchSvc{err: errors.New("HTTP error 401: unauthenticated")}
	err := runKBSearch(context.Background(), &KBSearchOptions{Query: "x", Limit: 20}, svc)
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeAuthUnauthenticated, typed.Code)
}

