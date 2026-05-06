package search

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

type fakeSearchService struct {
	results []*sdk.SearchResult
	err     error
	gotKB   string
	gotQ    string
}

func (f *fakeSearchService) HybridSearch(_ context.Context, kbID string, p *sdk.SearchParams) ([]*sdk.SearchResult, error) {
	f.gotKB = kbID
	f.gotQ = p.QueryText
	return f.results, f.err
}

func TestRunSearch_HumanOutput(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeSearchService{results: []*sdk.SearchResult{
		{Score: 0.92, Content: "first chunk", KnowledgeID: "doc-1", MatchType: sdk.MatchTypeVector},
		{Score: 0.81, Content: "second chunk", KnowledgeID: "doc-2", MatchType: sdk.MatchTypeKeyword},
	}}
	opts := &Options{Query: "hello", KBID: "kb_abc", TopK: 5}
	require.NoError(t, runSearch(context.Background(), opts, svc))

	assert.Equal(t, "kb_abc", svc.gotKB)
	assert.Equal(t, "hello", svc.gotQ)
	got := out.String()
	assert.Contains(t, got, "2 result(s) from kb=kb_abc")
	assert.Contains(t, got, "first chunk")
	assert.Contains(t, got, "doc-1")
}

// JSON envelope must surface match_type so machine consumers / agents can
// reason about retrieval channels without re-implementing the wire format.
// (Human renderer keeps default minimal — diagnostic info opt-in via --json,
// matching gh / kubectl / Algolia / Vespa terse-default conventions.)
func TestRunSearch_JSONIncludesMatchType(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeSearchService{results: []*sdk.SearchResult{
		{Score: 0.9, Content: "x", MatchType: sdk.MatchTypeKeyword},
	}}
	require.NoError(t, runSearch(context.Background(), &Options{Query: "q", KBID: "kb1", JSONOut: true}, svc))
	assert.Contains(t, out.String(), `"match_type":1`)
}

func TestRunSearch_JSONOutput(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeSearchService{results: []*sdk.SearchResult{{Score: 0.9, Content: "x"}}}
	opts := &Options{Query: "q", KBID: "kb1", TopK: 1, JSONOut: true}
	require.NoError(t, runSearch(context.Background(), opts, svc))
	assert.True(t, strings.HasPrefix(out.String(), `{"ok":true`), "got: %q", out.String())
	assert.Contains(t, out.String(), `"kb_id":"kb1"`)
}

func TestRunSearch_EmptyResults(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeSearchService{results: nil}
	require.NoError(t, runSearch(context.Background(), &Options{Query: "q", KBID: "kb1"}, svc))
	assert.Contains(t, out.String(), "(no results)")
}

// Server returns primary matches plus parent/related/nearby enrichment chunks,
// so the wire response can exceed TopK. CLI must trim to honor the user's
// hard-limit contract (gh / kubectl / aws idiom).
func TestRunSearch_TopKHardCap(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeSearchService{results: []*sdk.SearchResult{
		{Score: 0.9, Content: "primary 1"},
		{Score: 0.8, Content: "primary 2"},
		{Score: 0.7, Content: "primary 3"},
		{Score: 0, Content: "enrichment parent"}, // server-padded
		{Score: 0, Content: "enrichment nearby"}, // server-padded
	}}
	require.NoError(t, runSearch(context.Background(), &Options{Query: "q", KBID: "kb1", TopK: 3}, svc))
	got := out.String()
	assert.Contains(t, got, "3 result(s)")
	assert.NotContains(t, got, "enrichment parent")
	assert.NotContains(t, got, "enrichment nearby")
}

func TestRunSearch_BothChannelsDisabled(t *testing.T) {
	iostreams.SetForTest(t)
	err := runSearch(context.Background(), &Options{Query: "q", KBID: "kb1", NoVector: true, NoKeyword: true}, &fakeSearchService{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "input.invalid_argument")
}

func TestRunSearch_ServiceError_Transport(t *testing.T) {
	iostreams.SetForTest(t)
	svc := &fakeSearchService{err: assert.AnError}
	err := runSearch(context.Background(), &Options{Query: "q", KBID: "kb1"}, svc)
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeNetworkError, typed.Code,
		"non-HTTP-shaped errors classify as network.error so IsTransient picks them up")
}

func TestRunSearch_ServiceError_HTTPNotFound(t *testing.T) {
	iostreams.SetForTest(t)
	svc := &fakeSearchService{err: errors.New("HTTP error 404: knowledge base not found")}
	err := runSearch(context.Background(), &Options{Query: "q", KBID: "missing"}, svc)
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeResourceNotFound, typed.Code)
}

func TestIndent(t *testing.T) {
	assert.Equal(t, "  foo\n  bar", indent("foo\nbar", "  "))
	assert.Equal(t, "", indent("", "  "))
}

func TestRunSearch_NilService(t *testing.T) {
	iostreams.SetForTest(t)
	err := runSearch(context.Background(), &Options{Query: "q", KBID: "kb1"}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "server.error")
}

func TestNewCmdSearch_RequiresQuery(t *testing.T) {
	iostreams.SetForTest(t)
	cmd := NewCmdSearch(&cmdutil.Factory{
		Client: func() (*sdk.Client, error) { return nil, nil },
	})
	cmd.SetArgs([]string{}) // no query
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	err := cmd.Execute()
	require.Error(t, err)
}

func TestNewCmdSearch_RejectsEmptyQuery(t *testing.T) {
	iostreams.SetForTest(t)
	cmd := NewCmdSearch(&cmdutil.Factory{
		Client: func() (*sdk.Client, error) { return nil, nil },
	})
	cmd.SetArgs([]string{"  ", "--kb", "kb1"})
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "input.invalid_argument")
}

func TestRunSearch_NoVectorPassedThrough(t *testing.T) {
	iostreams.SetForTest(t)
	var got *sdk.SearchParams
	svc := &capturingSearchService{capture: func(p *sdk.SearchParams) { got = p }}
	require.NoError(t, runSearch(context.Background(), &Options{
		Query: "q", KBID: "kb1", NoVector: true,
	}, svc))
	require.NotNil(t, got)
	assert.True(t, got.DisableVectorMatch)
	assert.False(t, got.DisableKeywordsMatch)
}

func TestRunSearch_NoKeywordPassedThrough(t *testing.T) {
	iostreams.SetForTest(t)
	var got *sdk.SearchParams
	svc := &capturingSearchService{capture: func(p *sdk.SearchParams) { got = p }}
	require.NoError(t, runSearch(context.Background(), &Options{
		Query: "q", KBID: "kb1", NoKeyword: true,
	}, svc))
	require.NotNil(t, got)
	assert.True(t, got.DisableKeywordsMatch)
	assert.False(t, got.DisableVectorMatch)
}

type capturingSearchService struct {
	capture func(*sdk.SearchParams)
}

func (c *capturingSearchService) HybridSearch(_ context.Context, _ string, p *sdk.SearchParams) ([]*sdk.SearchResult, error) {
	c.capture(p)
	return nil, nil
}
