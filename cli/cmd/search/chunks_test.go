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

type fakeChunksSvc struct {
	results []*sdk.SearchResult
	err     error
	gotKB   string
	gotQ    string
}

func (f *fakeChunksSvc) HybridSearch(_ context.Context, kbID string, p *sdk.SearchParams) ([]*sdk.SearchResult, error) {
	f.gotKB = kbID
	f.gotQ = p.QueryText
	return f.results, f.err
}

func TestRunSearch_HumanOutput(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeChunksSvc{results: []*sdk.SearchResult{
		{Score: 0.92, Content: "first chunk", KnowledgeID: "doc-1", MatchType: sdk.MatchTypeVector},
		{Score: 0.81, Content: "second chunk", KnowledgeID: "doc-2", MatchType: sdk.MatchTypeKeyword},
	}}
	opts := &ChunksOptions{Query: "hello", KBID: "kb_abc", Limit: 5}
	require.NoError(t, runChunks(context.Background(), opts, nil, svc))

	assert.Equal(t, "kb_abc", svc.gotKB)
	assert.Equal(t, "hello", svc.gotQ)
	got := out.String()
	assert.Contains(t, got, "2 result(s) from kb=kb_abc")
	assert.Contains(t, got, "first chunk")
	assert.Contains(t, got, "doc-1")
}

// JSON output must surface match_type so machine consumers / agents can
// reason about retrieval channels without re-implementing the wire format.
// (Human renderer keeps default minimal - diagnostic info opt-in via --json.)
func TestRunSearch_JSONIncludesMatchType(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeChunksSvc{results: []*sdk.SearchResult{
		{Score: 0.9, Content: "x", MatchType: sdk.MatchTypeKeyword},
	}}
	require.NoError(t, runChunks(context.Background(), &ChunksOptions{Query: "q", KBID: "kb1"}, &cmdutil.JSONOptions{}, svc))
	assert.Contains(t, out.String(), `"match_type":1`)
}

func TestRunSearch_JSONOutput(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeChunksSvc{results: []*sdk.SearchResult{{Score: 0.9, Content: "x"}}}
	opts := &ChunksOptions{Query: "q", KBID: "kb1", Limit: 1}
	require.NoError(t, runChunks(context.Background(), opts, &cmdutil.JSONOptions{}, svc))
	got := out.String()
	assert.True(t, strings.HasPrefix(strings.TrimSpace(got), "["), "expected bare JSON array, got: %q", got)
	assert.NotContains(t, got, `"ok":`)
	assert.Contains(t, got, `"score":0.9`)
}

func TestRunSearch_EmptyResults(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeChunksSvc{results: nil}
	require.NoError(t, runChunks(context.Background(), &ChunksOptions{Query: "q", KBID: "kb1"}, nil, svc))
	assert.Contains(t, out.String(), "(no results)")
}

// Server returns primary matches plus parent/related/nearby enrichment chunks,
// so the wire response can exceed Limit. CLI must trim to honor the user's
// hard-limit contract.
func TestRunSearch_LimitHardCap(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeChunksSvc{results: []*sdk.SearchResult{
		{Score: 0.9, Content: "primary 1"},
		{Score: 0.8, Content: "primary 2"},
		{Score: 0.7, Content: "primary 3"},
		{Score: 0, Content: "enrichment parent"}, // server-padded
		{Score: 0, Content: "enrichment nearby"}, // server-padded
	}}
	require.NoError(t, runChunks(context.Background(), &ChunksOptions{Query: "q", KBID: "kb1", Limit: 3}, nil, svc))
	got := out.String()
	assert.Contains(t, got, "3 result(s)")
	assert.NotContains(t, got, "enrichment parent")
	assert.NotContains(t, got, "enrichment nearby")
}

func TestRunSearch_BothChannelsDisabled(t *testing.T) {
	iostreams.SetForTest(t)
	err := runChunks(context.Background(), &ChunksOptions{Query: "q", KBID: "kb1", NoVector: true, NoKeyword: true}, nil, &fakeChunksSvc{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "input.invalid_argument")
}

func TestRunSearch_ServiceError_Transport(t *testing.T) {
	iostreams.SetForTest(t)
	svc := &fakeChunksSvc{err: assert.AnError}
	err := runChunks(context.Background(), &ChunksOptions{Query: "q", KBID: "kb1"}, nil, svc)
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeNetworkError, typed.Code,
		"non-HTTP-shaped errors classify as network.error so IsTransient picks them up")
}

func TestRunSearch_ServiceError_HTTPNotFound(t *testing.T) {
	iostreams.SetForTest(t)
	svc := &fakeChunksSvc{err: errors.New("HTTP error 404: knowledge base not found")}
	err := runChunks(context.Background(), &ChunksOptions{Query: "q", KBID: "missing"}, nil, svc)
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
	err := runChunks(context.Background(), &ChunksOptions{Query: "q", KBID: "kb1"}, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "server.error")
}

func TestNewCmdChunks_RequiresQuery(t *testing.T) {
	iostreams.SetForTest(t)
	cmd := NewCmdChunks(&cmdutil.Factory{
		Client: func() (*sdk.Client, error) { return nil, nil },
	})
	cmd.SetArgs([]string{}) // no query
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	err := cmd.Execute()
	require.Error(t, err)
}

func TestNewCmdChunks_RejectsEmptyQuery(t *testing.T) {
	iostreams.SetForTest(t)
	cmd := NewCmdChunks(&cmdutil.Factory{
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
	svc := &capturingChunksSvc{capture: func(p *sdk.SearchParams) { got = p }}
	require.NoError(t, runChunks(context.Background(), &ChunksOptions{
		Query: "q", KBID: "kb1", NoVector: true,
	}, nil, svc))
	require.NotNil(t, got)
	assert.True(t, got.DisableVectorMatch)
	assert.False(t, got.DisableKeywordsMatch)
}

func TestRunSearch_NoKeywordPassedThrough(t *testing.T) {
	iostreams.SetForTest(t)
	var got *sdk.SearchParams
	svc := &capturingChunksSvc{capture: func(p *sdk.SearchParams) { got = p }}
	require.NoError(t, runChunks(context.Background(), &ChunksOptions{
		Query: "q", KBID: "kb1", NoKeyword: true,
	}, nil, svc))
	require.NotNil(t, got)
	assert.True(t, got.DisableKeywordsMatch)
	assert.False(t, got.DisableVectorMatch)
}

type capturingChunksSvc struct {
	capture func(*sdk.SearchParams)
}

func (c *capturingChunksSvc) HybridSearch(_ context.Context, _ string, p *sdk.SearchParams) ([]*sdk.SearchResult, error) {
	c.capture(p)
	return nil, nil
}
