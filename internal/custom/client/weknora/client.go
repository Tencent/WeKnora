// Package weknora 提供 WeKnora 内容引擎客户端（VP-T009 字幕分块入库）。
//
// 设计要点：
//   - 内容（字幕分块文本 + 11 字段 metadata）走 WeKnora 手工知识 API 入 KB
//   - 自研后端只保存 WeKnora 返回的 knowledge ID 到 videos 表（单一数据源）
//   - 端点以 WeKnora 0.7.2 公开 REST 为准；后续若调整，仅改本文件
package weknora

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/custom/config"
)

// Client WeKnora HTTP 客户端
type Client struct {
	baseURL  string
	apiKey   string
	kbID     string
	tenantID string
	http     *http.Client
}

// New 构造 WeKnora client
func New(cfg config.WeKnoraConfig) *Client {
	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	baseURL = strings.TrimSuffix(baseURL, "/api/v1")
	return &Client{
		baseURL:  baseURL,
		apiKey:   cfg.APIKey,
		kbID:     cfg.KBID,
		tenantID: cfg.TenantID,
		http:     &http.Client{Timeout: 60 * time.Second},
	}
}

// KBID 返回默认入库目标 KB
func (c *Client) KBID() string { return c.kbID }

// ManualKnowledgeInput 是 WeKnora 手工 Markdown 知识的公开请求契约。
type ManualKnowledgeInput struct {
	Title   string `json:"title"`
	Content string `json:"content"`
	Status  string `json:"status"`
	Channel string `json:"channel"`
}

// ManualKnowledgeResult 是创建手工知识后需要的最小响应字段。
type ManualKnowledgeResult struct {
	ID              string `json:"id"`
	KnowledgeBaseID string `json:"knowledge_base_id"`
	Title           string `json:"title"`
	ParseStatus     string `json:"parse_status"`
	ErrorMessage    string `json:"error_message"`
}

type SearchParams struct {
	QueryText            string   `json:"query_text"`
	VectorThreshold      float64  `json:"vector_threshold,omitempty"`
	KeywordThreshold     float64  `json:"keyword_threshold,omitempty"`
	MatchCount           int      `json:"match_count,omitempty"`
	DisableKeywordsMatch bool     `json:"disable_keywords_match,omitempty"`
	DisableVectorMatch   bool     `json:"disable_vector_match,omitempty"`
	KnowledgeIDs         []string `json:"knowledge_ids,omitempty"`
}

type SearchResult struct {
	ID          string `json:"id"`
	KnowledgeID string `json:"knowledge_id"`
	Content     string `json:"content"`
}

// HybridSearch 通过真实检索接口确认知识已进入可检索索引。
func (c *Client) HybridSearch(ctx context.Context, kbID string, params SearchParams) ([]SearchResult, error) {
	if kbID == "" {
		kbID = c.kbID
	}
	if kbID == "" {
		return nil, fmt.Errorf("weknora kb_id 未配置")
	}
	if strings.TrimSpace(params.QueryText) == "" {
		return nil, fmt.Errorf("weknora search query 不能为空")
	}
	body, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("encode weknora search request: %w", err)
	}
	u := fmt.Sprintf("%s/api/v1/knowledge-bases/%s/hybrid-search", strings.TrimRight(c.baseURL, "/"), kbID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	c.setHeaders(req)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("weknora hybrid search: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		buf, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("weknora hybrid search status %d: %s", resp.StatusCode, string(buf))
	}
	var out struct {
		Success bool           `json:"success"`
		Data    []SearchResult `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode weknora search response: %w", err)
	}
	if !out.Success {
		return nil, fmt.Errorf("weknora hybrid search returned unsuccessful response")
	}
	return out.Data, nil
}

func (c *Client) IsKnowledgeSearchable(ctx context.Context, kbID, knowledgeID string) (bool, error) {
	results, err := c.HybridSearch(ctx, kbID, SearchParams{
		QueryText:            "视频定位信息",
		MatchCount:           5,
		DisableVectorMatch:   true,
		DisableKeywordsMatch: false,
		KnowledgeIDs:         []string{knowledgeID},
	})
	if err != nil {
		return false, err
	}
	for _, result := range results {
		if result.KnowledgeID == knowledgeID && strings.TrimSpace(result.Content) != "" {
			return true, nil
		}
	}
	return false, nil
}

// CreateManualKnowledge 通过 WeKnora 公开接口创建一条手工 Markdown 知识。
func (c *Client) CreateManualKnowledge(ctx context.Context, kbID string, input ManualKnowledgeInput) (ManualKnowledgeResult, error) {
	if kbID == "" {
		kbID = c.kbID
	}
	if kbID == "" {
		return ManualKnowledgeResult{}, fmt.Errorf("weknora kb_id 未配置")
	}
	if strings.TrimSpace(input.Title) == "" || strings.TrimSpace(input.Content) == "" {
		return ManualKnowledgeResult{}, fmt.Errorf("manual knowledge title/content 不能为空")
	}
	if input.Status == "" {
		input.Status = "publish"
	}
	if input.Channel == "" {
		input.Channel = "api"
	}
	body, err := json.Marshal(input)
	if err != nil {
		return ManualKnowledgeResult{}, err
	}
	url := fmt.Sprintf("%s/api/v1/knowledge-bases/%s/knowledge/manual", strings.TrimRight(c.baseURL, "/"), kbID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return ManualKnowledgeResult{}, err
	}
	c.setHeaders(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return ManualKnowledgeResult{}, fmt.Errorf("weknora manual ingest: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		buf, _ := io.ReadAll(resp.Body)
		return ManualKnowledgeResult{}, fmt.Errorf("weknora manual ingest status %d: %s", resp.StatusCode, string(buf))
	}
	var out struct {
		Success bool                  `json:"success"`
		Data    ManualKnowledgeResult `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return ManualKnowledgeResult{}, fmt.Errorf("decode weknora response: %w", err)
	}
	if !out.Success || out.Data.ID == "" {
		return ManualKnowledgeResult{}, fmt.Errorf("weknora manual ingest returned empty knowledge id")
	}
	return out.Data, nil
}

// FindManualKnowledgeByTitle 用稳定标题对已成功但未写入本地检查点的请求做重试对账。
func (c *Client) FindManualKnowledgeByTitle(ctx context.Context, kbID, title string) (*ManualKnowledgeResult, error) {
	if kbID == "" {
		kbID = c.kbID
	}
	query := url.Values{"page": {"1"}, "page_size": {"100"}, "file_type": {"manual"}, "keyword": {title}}
	u := fmt.Sprintf("%s/api/v1/knowledge-bases/%s/knowledge?%s", strings.TrimRight(c.baseURL, "/"), kbID, query.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	c.setHeaders(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("weknora list manual knowledge: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		buf, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("weknora list manual knowledge status %d: %s", resp.StatusCode, string(buf))
	}
	var out struct {
		Data []ManualKnowledgeResult `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode weknora knowledge list: %w", err)
	}
	for _, item := range out.Data {
		if item.Title == title {
			found := item
			return &found, nil
		}
	}
	return nil, nil
}

// GetKnowledge 读取单条知识的解析状态。
func (c *Client) GetKnowledge(ctx context.Context, knowledgeID string) (ManualKnowledgeResult, error) {
	u := fmt.Sprintf("%s/api/v1/knowledge/%s", strings.TrimRight(c.baseURL, "/"), url.PathEscape(knowledgeID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return ManualKnowledgeResult{}, err
	}
	c.setHeaders(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return ManualKnowledgeResult{}, fmt.Errorf("weknora get knowledge: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		buf, _ := io.ReadAll(resp.Body)
		return ManualKnowledgeResult{}, fmt.Errorf("weknora get knowledge status %d: %s", resp.StatusCode, string(buf))
	}
	var out struct {
		Success bool                  `json:"success"`
		Data    ManualKnowledgeResult `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return ManualKnowledgeResult{}, fmt.Errorf("decode weknora knowledge: %w", err)
	}
	if !out.Success || out.Data.ID == "" {
		return ManualKnowledgeResult{}, fmt.Errorf("weknora get knowledge returned empty id")
	}
	return out.Data, nil
}

// DeleteKnowledge 删除 KB（VP-T011 删除视频级联清理用）
func (c *Client) DeleteKnowledge(ctx context.Context, knowledgeID string) error {
	url := fmt.Sprintf("%s/api/v1/knowledge/%s", strings.TrimRight(c.baseURL, "/"), url.PathEscape(knowledgeID))
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return err
	}
	c.setHeaders(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 && resp.StatusCode != http.StatusNotFound {
		buf, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("weknora delete status %d: %s", resp.StatusCode, string(buf))
	}
	return nil
}

func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("X-API-Key", c.apiKey)
	}
	if c.tenantID != "" {
		req.Header.Set("X-Tenant-ID", c.tenantID)
	}
}
