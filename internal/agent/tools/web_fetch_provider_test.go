package tools

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type fetchPage struct {
	mu   sync.Mutex
	urls []string
}

func (p *fetchPage) Fetch(_ context.Context, pageURL string) (*types.WebPage, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.urls = append(p.urls, pageURL)
	return &types.WebPage{Content: "# remote " + pageURL, Source: "remote"}, nil
}

func TestWebFetchReadsThroughProviderFetcher(t *testing.T) {
	page := &fetchPage{}
	tool := NewWebFetchTool(page)

	result, err := tool.Execute(context.Background(), json.RawMessage(
		`{"items":[{"url":"https://example.com/a"},{"url":"https://example.com/b"}]}`))
	require.NoError(t, err)
	require.True(t, result.Success)
	require.ElementsMatch(t, []string{"https://example.com/a", "https://example.com/b"}, page.urls)
	require.Contains(t, result.Output, "# remote https://example.com/a")
}
