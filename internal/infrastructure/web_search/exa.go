package web_search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	defaultExaSearchURL = "https://api.exa.ai/search"
	defaultExaTimeout   = 15 * time.Second
	defaultExaResults   = 5
	maxExaResults       = 100
	maxExaResponseBytes = 2 << 20
	maxExaContentRunes  = 12000
)

// ExaProvider implements web search using Exa's official Search API.
type ExaProvider struct {
	client      *http.Client
	baseURL     string
	apiKey      string
	includeText bool
}

// NewExaProvider creates an Exa provider from tenant-specific parameters.
func NewExaProvider(params types.WebSearchProviderParameters) (interfaces.WebSearchProvider, error) {
	apiKey := strings.TrimSpace(params.APIKey)
	if apiKey == "" {
		return nil, fmt.Errorf("API key is required for Exa provider")
	}
	client, err := NewSearchHTTPClient(defaultExaTimeout, params.ProxyURL)
	if err != nil {
		return nil, err
	}
	return &ExaProvider{
		client:      client,
		baseURL:     defaultExaSearchURL,
		apiKey:      apiKey,
		includeText: parseExaBool(params.ExtraConfig, "include_text"),
	}, nil
}

// Name returns the provider type identifier.
func (p *ExaProvider) Name() string { return "exa" }

// Search performs a web search through Exa's official Search API.
func (p *ExaProvider) Search(
	ctx context.Context,
	query string,
	maxResults int,
	includeDate bool,
) ([]*types.WebSearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("query is empty")
	}
	if maxResults <= 0 {
		maxResults = defaultExaResults
	}
	if maxResults > maxExaResults {
		maxResults = maxExaResults
	}

	bodyBytes, err := json.Marshal(exaSearchRequest{
		Query:      query,
		NumResults: maxResults,
		Contents: exaContents{
			Highlights: true,
			Text:       p.includeText,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal Exa request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create Exa request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", p.apiKey)

	logger.Infof(ctx, "[WebSearch][Exa] query=%q maxResults=%d url=%s", query, maxResults, p.baseURL)
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute Exa request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxExaResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to read Exa response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		logger.Warnf(ctx, "[WebSearch][Exa] API returned status %d: %s", resp.StatusCode, string(body))
		return nil, fmt.Errorf("exa API returned status %d: %s", resp.StatusCode, string(body))
	}

	var data exaSearchResponse
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("failed to unmarshal Exa response: %w", err)
	}
	if data.Error != "" {
		return nil, fmt.Errorf("exa API error: %s", data.Error)
	}

	results := make([]*types.WebSearchResult, 0, len(data.Results))
	for _, item := range data.Results {
		if len(results) >= maxResults {
			break
		}
		snippet := strings.TrimSpace(strings.Join(item.Highlights, "\n"))
		content := truncateExaText(strings.TrimSpace(item.Text), maxExaContentRunes)
		if snippet == "" {
			snippet = truncateExaText(content, 500)
		}
		result := &types.WebSearchResult{Title: item.Title, URL: item.URL, Snippet: snippet, Content: content, Source: "exa"}
		if includeDate && item.PublishedDate != "" {
			if publishedAt, err := time.Parse(time.RFC3339, item.PublishedDate); err == nil {
				result.PublishedAt = &publishedAt
			}
		}
		results = append(results, result)
	}
	logger.Infof(ctx, "[WebSearch][Exa] returned %d results", len(results))
	return results, nil
}

func parseExaBool(config map[string]string, key string) bool {
	v, err := strconv.ParseBool(strings.TrimSpace(config[key]))
	return err == nil && v
}

func truncateExaText(value string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	if utf8.RuneCountInString(value) <= maxRunes {
		return value
	}
	byteIndex := 0
	for runeIndex := 0; runeIndex < maxRunes && byteIndex < len(value); runeIndex++ {
		_, size := utf8.DecodeRuneInString(value[byteIndex:])
		byteIndex += size
	}
	return value[:byteIndex]
}

type exaSearchRequest struct {
	Query      string      `json:"query"`
	NumResults int         `json:"numResults"`
	Contents   exaContents `json:"contents"`
}

type exaContents struct {
	Highlights bool `json:"highlights"`
	Text       bool `json:"text,omitempty"`
}

type exaSearchResponse struct {
	Results []exaResult `json:"results"`
	Error   string      `json:"error,omitempty"`
}

type exaResult struct {
	Title         string   `json:"title"`
	URL           string   `json:"url"`
	PublishedDate string   `json:"publishedDate,omitempty"`
	Highlights    []string `json:"highlights,omitempty"`
	Text          string   `json:"text,omitempty"`
}
