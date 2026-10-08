package web_search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	defaultMetasoSearchURL = "https://metaso.cn/api/v1/search"
	defaultMetasoTimeout   = 30 * time.Second
	defaultMetasoResults   = 10
	maxMetasoResults       = 50
	maxMetasoResponseBytes = 4 << 20
	defaultMetasoScope     = "webpage"
)

var validMetasoScopes = map[string]struct{}{
	"webpage": {}, "document": {}, "scholar": {},
	"podcast": {}, "video": {}, "image": {},
}

// MetasoProvider implements web search using the official Metaso AI Search API.
type MetasoProvider struct {
	client  *http.Client
	baseURL string
	apiKey  string
	scope   string
}

func NewMetasoProvider(params types.WebSearchProviderParameters) (interfaces.WebSearchProvider, error) {
	if err := ValidateMetasoParameters(params); err != nil {
		return nil, err
	}
	client, err := NewSearchHTTPClient(defaultMetasoTimeout, params.ProxyURL)
	if err != nil {
		return nil, err
	}
	return &MetasoProvider{
		client: client, baseURL: defaultMetasoSearchURL,
		apiKey: strings.TrimSpace(params.APIKey), scope: metasoScope(params.ExtraConfig),
	}, nil
}

func ValidateMetasoParameters(params types.WebSearchProviderParameters) error {
	if strings.TrimSpace(params.APIKey) == "" {
		return fmt.Errorf("API key is required for Metaso provider")
	}
	scope := metasoScope(params.ExtraConfig)
	if _, ok := validMetasoScopes[scope]; !ok {
		return fmt.Errorf("invalid Metaso search scope: %s", scope)
	}
	return nil
}

func metasoScope(extraConfig map[string]string) string {
	if scope := strings.TrimSpace(extraConfig["scope"]); scope != "" {
		return scope
	}
	return defaultMetasoScope
}

func (p *MetasoProvider) Name() string { return "metaso" }

func (p *MetasoProvider) Search(ctx context.Context, query string, maxResults int, includeDate bool) ([]*types.WebSearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("query is empty")
	}
	if maxResults <= 0 {
		maxResults = defaultMetasoResults
	}
	if maxResults > maxMetasoResults {
		maxResults = maxMetasoResults
	}

	body, err := json.Marshal(metasoSearchRequest{
		Query: query, Scope: p.scope, Size: maxResults,
		IncludeSummary: true, IncludeRawContent: false, ConciseSnippet: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal Metaso request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create Metaso request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	logger.Infof(ctx, "[WebSearch][Metaso] query=%q maxResults=%d scope=%s", query, maxResults, p.scope)
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute Metaso request: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := readMetasoResponseBody(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, metasoHTTPError(resp.StatusCode, respBody)
	}

	var response metasoSearchResponse
	if err := json.Unmarshal(respBody, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal Metaso response: %w", err)
	}
	// Metaso 的结果数组键名随 scope 变化（webpages/documents/scholars/…），
	// 只读 webpages 会让任何 scope != webpage 的配置永久返回 0 条。
	items, arrayName := response.itemsForScope(p.scope)
	if _, ok := validMetasoScopes[p.scope]; !ok {
		logger.Warnf(ctx, "[WebSearch][Metaso] unknown scope %q; fell back to the %q array",
			p.scope, arrayName)
	}
	results := make([]*types.WebSearchResult, 0, len(items))
	for _, item := range items {
		link := item.location()
		if strings.TrimSpace(item.Title) == "" && link == "" {
			continue
		}
		snippet := strings.TrimSpace(item.Summary)
		if snippet == "" {
			snippet = strings.TrimSpace(item.Snippet)
		}
		result := &types.WebSearchResult{
			Title: item.Title, URL: link, Snippet: snippet,
			Content: item.RawContent, Source: "metaso",
		}
		if includeDate {
			if publishedAt, ok := parseMetasoDate(item.Date); ok {
				result.PublishedAt = &publishedAt
			}
		}
		results = append(results, result)
		if len(results) >= maxResults {
			break
		}
	}
	logger.Infof(ctx, "[WebSearch][Metaso] scope=%s array=%s returned=%d",
		p.scope, arrayName, len(results))
	return results, nil
}

func readMetasoResponseBody(reader io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, maxMetasoResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("failed to read Metaso response: %w", err)
	}
	if len(body) > maxMetasoResponseBytes {
		return nil, fmt.Errorf("Metaso response exceeds %d bytes", maxMetasoResponseBytes)
	}
	return body, nil
}

func metasoHTTPError(statusCode int, body []byte) error {
	var apiError struct {
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	if json.Unmarshal(body, &apiError) == nil {
		detail := strings.TrimSpace(apiError.Message)
		if detail == "" {
			detail = strings.TrimSpace(apiError.Error)
		}
		if detail != "" {
			return fmt.Errorf("Metaso API returned status %d: %s", statusCode, detail)
		}
	}
	detail := strings.TrimSpace(string(body))
	if len(detail) > 4096 {
		detail = detail[:4096]
	}
	if detail == "" {
		return fmt.Errorf("Metaso API returned status %d", statusCode)
	}
	return fmt.Errorf("Metaso API returned status %d: %s", statusCode, detail)
}

func parseMetasoDate(value string) (time.Time, bool) {
	for _, layout := range []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05",
		"2006-01-02",
		"2006年01月02日", // webpage scope 实际返回的日期格式
		"2006/01/02",
	} {
		if parsed, err := time.Parse(layout, strings.TrimSpace(value)); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

type metasoSearchRequest struct {
	Query             string `json:"q"`
	Scope             string `json:"scope"`
	Size              int    `json:"size"`
	IncludeSummary    bool   `json:"includeSummary"`
	IncludeRawContent bool   `json:"includeRawContent"`
	ConciseSnippet    bool   `json:"conciseSnippet"`
}

// metasoSearchResponse 只声明各 scope 对应的结果数组。
//
// Metaso 把结果放在与 scope 同名的键下（webpage→webpages、scholar→scholars…），
// 因此每个 scope 都要有自己的字段，读错键就会得到 0 条结果。
type metasoSearchResponse struct {
	Webpages  []metasoWebpage `json:"webpages"`
	Documents []metasoWebpage `json:"documents"`
	Scholars  []metasoWebpage `json:"scholars"`
	Podcasts  []metasoWebpage `json:"podcasts"`
	Videos    []metasoWebpage `json:"videos"`
	Images    []metasoWebpage `json:"images"`
}

// itemsForScope 按 scope 取对应的结果数组，并返回数组名用于日志。
//
// 实测（https://metaso.cn/api/v1/search）响应键名与 scope 一一对应：
// webpage→webpages、document→documents、scholar→scholars、
// podcast→podcasts、video→videos、image→images。
func (r metasoSearchResponse) itemsForScope(scope string) ([]metasoWebpage, string) {
	switch scope {
	case "document":
		return r.Documents, "documents"
	case "scholar":
		return r.Scholars, "scholars"
	case "podcast":
		return r.Podcasts, "podcasts"
	case "video":
		return r.Videos, "videos"
	case "image":
		return r.Images, "images"
	case "webpage":
		return r.Webpages, "webpages"
	}
	// 未知 scope：回退到第一个非空数组，尽量不丢结果
	for _, candidate := range []struct {
		name  string
		items []metasoWebpage
	}{
		{"webpages", r.Webpages},
		{"documents", r.Documents},
		{"scholars", r.Scholars},
		{"podcasts", r.Podcasts},
		{"videos", r.Videos},
		{"images", r.Images},
	} {
		if len(candidate.items) > 0 {
			return candidate.items, candidate.name
		}
	}
	return nil, "webpages"
}

// metasoWebpage 只声明真正会被读取的字段。
//
// 响应里还有 score/position/authors/duration/coverImage/imageWidth/imageHeight
// 等键，此处刻意不建模：多声明一个类型不符的字段，整次搜索就会因反序列化
// 失败而返回 0 条，而多出来的键本来就会被 encoding/json 忽略。
type metasoWebpage struct {
	Title      string `json:"title"`
	Link       string `json:"link"`
	URL        string `json:"url"`
	Snippet    string `json:"snippet"`
	Summary    string `json:"summary"`
	RawContent string `json:"rawContent"`
	Date       string `json:"date"`
	ImageURL   string `json:"imageUrl"`
}

// location 返回条目的可访问地址。
// image scope 的条目没有 link，只有 imageUrl；若只认 link，
// 这些条目会被 agent 侧的 URL 校验丢弃，导致「有结果但返回 0 条」。
func (w metasoWebpage) location() string {
	for _, candidate := range []string{w.Link, w.URL, w.ImageURL} {
		if trimmed := strings.TrimSpace(candidate); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
