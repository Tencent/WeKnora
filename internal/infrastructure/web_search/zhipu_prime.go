package web_search

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/mcp"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	// Fixed official endpoint; tenant credentials must never be sent to a custom URL.
	defaultZhipuPrimeURL = "https://open.bigmodel.cn/api/mcp/web_search_prime/mcp"
	zhipuPrimeTimeout    = 30 * time.Second
)

// ZhipuPrimeProvider uses the GLM Coding Plan search MCP service.
type ZhipuPrimeProvider struct {
	apiKey   string
	endpoint string
}

// NewZhipuPrimeProvider creates a provider using the existing encrypted API key parameters.
func NewZhipuPrimeProvider(params types.WebSearchProviderParameters) (interfaces.WebSearchProvider, error) {
	if err := ValidateZhipuPrimeParameters(params); err != nil {
		return nil, err
	}
	return &ZhipuPrimeProvider{apiKey: strings.TrimSpace(params.APIKey), endpoint: defaultZhipuPrimeURL}, nil
}

// ValidateZhipuPrimeParameters validates Coding Plan credentials and supported options.
func ValidateZhipuPrimeParameters(params types.WebSearchProviderParameters) error {
	if strings.TrimSpace(params.APIKey) == "" {
		return fmt.Errorf("coding plan API key is required for Zhipu Prime provider")
	}
	if strings.TrimSpace(params.ProxyURL) != "" {
		return fmt.Errorf("zhipu prime provider does not support a per-provider proxy_url")
	}
	return nil
}

// Name returns the provider identifier.
func (p *ZhipuPrimeProvider) Name() string { return "zhipu_prime" }

// Search uses a separate MCP session per call so credentials and sessions are never shared across workspaces.
func (p *ZhipuPrimeProvider) Search(
	ctx context.Context, query string, maxResults int, includeDate bool,
) ([]*types.WebSearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("search query cannot be empty")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if maxResults <= 0 {
		maxResults = defaultZhipuResults
	}
	if maxResults > maxZhipuResults {
		maxResults = maxZhipuResults
	}
	ctx, cancel := context.WithTimeout(ctx, zhipuPrimeTimeout)
	defer cancel()
	client, err := mcp.NewMCPClient(&mcp.ClientConfig{Service: &types.MCPService{
		ID: "zhipu-prime-search", Name: "Zhipu Prime Search", URL: &p.endpoint,
		TransportType:  types.MCPTransportHTTPStreamable,
		AuthConfig:     &types.MCPAuthConfig{AuthType: types.MCPAuthBearer, Token: p.apiKey},
		AdvancedConfig: &types.MCPAdvancedConfig{Timeout: int(zhipuPrimeTimeout / time.Second)},
	}})
	if err != nil {
		return nil, fmt.Errorf("create Zhipu Prime MCP client: %w", err)
	}
	defer func() { _ = client.Disconnect() }()
	if err := client.Connect(ctx); err != nil {
		return nil, fmt.Errorf("connect Zhipu Prime MCP: %w", err)
	}
	if _, err := client.Initialize(ctx); err != nil {
		return nil, fmt.Errorf("initialize Zhipu Prime MCP: %w", err)
	}
	tools, err := client.ListTools(ctx)
	if err != nil {
		return nil, fmt.Errorf("list Zhipu Prime MCP tools: %w", err)
	}
	name, args, err := zhipuPrimeTool(tools, query, maxResults)
	if err != nil {
		return nil, err
	}
	response, err := client.CallTool(ctx, name, args)
	if err != nil {
		return nil, fmt.Errorf("call Zhipu Prime search: %w", err)
	}
	if response == nil || response.IsError {
		return nil, fmt.Errorf("zhipu prime search failed; check Coding Plan credentials and search quota")
	}
	results := make([]*types.WebSearchResult, 0, maxResults)
	foundPayload := false
	for _, content := range response.Content {
		if content.Type != "text" || strings.TrimSpace(content.Text) == "" {
			continue
		}
		items, err := parseZhipuPrimeResults(content.Text)
		if err != nil {
			return nil, err
		}
		foundPayload = true
		for _, item := range items {
			if strings.TrimSpace(item.Link) == "" {
				continue
			}
			result := &types.WebSearchResult{
				Title: item.Title, URL: item.Link, Snippet: item.Content, Source: p.Name(),
			}
			if includeDate {
				if publishedAt, ok := parseZhipuDate(item.PublishDate); ok {
					result.PublishedAt = &publishedAt
				}
			}
			results = append(results, result)
			if len(results) >= maxResults {
				return results, nil
			}
		}
	}
	if !foundPayload {
		return nil, fmt.Errorf("zhipu prime MCP response contains no search payload")
	}
	return results, nil
}

func zhipuPrimeTool(tools []*types.MCPTool, query string, count int) (string, map[string]interface{}, error) {
	// The documented camelCase name and the newer snake_case name share the same search input.
	for _, name := range []string{"webSearchPrime", "web_search_prime"} {
		for _, tool := range tools {
			if tool == nil || tool.Name != name {
				continue
			}
			var schema struct {
				Properties map[string]json.RawMessage `json:"properties"`
			}
			if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
				return "", nil, fmt.Errorf("invalid Zhipu Prime tool schema: %w", err)
			}
			args := map[string]interface{}{"search_query": query}
			// Some versions expose only search_query and filters; always enforce the count locally.
			if _, ok := schema.Properties["count"]; ok {
				args["count"] = count
			}
			return name, args, nil
		}
	}
	return "", nil, fmt.Errorf("zhipu prime MCP server does not expose a supported search tool")
}

func parseZhipuPrimeResults(text string) ([]zhipuSearchResult, error) {
	if len(text) > maxZhipuResponseBytes {
		return nil, fmt.Errorf("zhipu prime search payload exceeds %d bytes", maxZhipuResponseBytes)
	}
	// MCP text can contain an array directly, or a JSON-encoded string holding that array.
	for depth := 0; depth < 3; depth++ {
		text = strings.TrimSpace(text)
		if strings.HasPrefix(text, "\"") {
			var decoded string
			if err := json.Unmarshal([]byte(text), &decoded); err != nil {
				return nil, fmt.Errorf("invalid Zhipu Prime search payload: %w", err)
			}
			text = decoded
			continue
		}
		if strings.HasPrefix(text, "{") {
			var envelope struct {
				SearchResult json.RawMessage `json:"search_result"`
			}
			if err := json.Unmarshal([]byte(text), &envelope); err != nil {
				return nil, fmt.Errorf("invalid Zhipu Prime search payload: %w", err)
			}
			text = strings.TrimSpace(string(envelope.SearchResult))
		}
		if !strings.HasPrefix(text, "[") {
			break
		}
		var results []zhipuSearchResult
		if err := json.Unmarshal([]byte(text), &results); err != nil {
			return nil, fmt.Errorf("invalid Zhipu Prime search results: %w", err)
		}
		return results, nil
	}
	return nil, fmt.Errorf("zhipu prime search payload is not a results array")
}
