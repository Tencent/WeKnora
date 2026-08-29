// Package weknora Wiki 读 API（CP-T008 / CP-T009 聚合展示用）。
//
// 端点（来自 WeKnora 0.7.2 handler/wiki_page.go）：
//   - GET /api/v1/knowledgebase/{kb_id}/wiki/pages             列出 KB 内 Wiki 页面
//   - GET /api/v1/knowledgebase/{kb_id}/wiki/pages/{slug}      读单个 Wiki 页内容
//
// 设计要点：
//   - 聚合 API（CP-T008 / CP-T009）按 page_type + frontmatter.type 过滤
//   - 不解析 Neo4j 节点；跨视频边在 CP-T008 由自研后端从 Neo4j pipeline 取
package weknora

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/custom/config"
	"gopkg.in/yaml.v3"
)

// WikiClient Wiki 读客户端
type WikiClient struct {
	cfg  config.WeKnoraConfig
	http *http.Client
}

// NewWikiClient 构造
func NewWikiClient(cfg config.WeKnoraConfig) *WikiClient {
	return &WikiClient{
		cfg:  cfg,
		http: &http.Client{Timeout: 60 * time.Second},
	}
}

// WikiPage Wiki 页面（读 API 返回的最小可用结构）
//
// 注意：WeKnora 原生 Wiki 页 API 不返回 frontmatter 字段——agent 写入的 YAML
// frontmatter 存在 content 顶部。本侧通过 ParsedFrontmatter() 自行解析。
type WikiPage struct {
	ID             string    `json:"id"`
	Slug           string    `json:"slug"`
	Title          string    `json:"title"`
	PageType       string    `json:"page_type"` // 6 值白名单之一
	Content        string    `json:"content"`
	Summary        string    `json:"summary,omitempty"`
	Version        int       `json:"version"`
	LastEditSource string    `json:"last_edit_source"`
	LastEditorID   string    `json:"last_editor_id"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type WikiPageWrite struct {
	Slug     string
	Title    string
	PageType string
	Status   string
	Content  string
	Summary  string
	Version  int
}

// ParsedFrontmatter 从 content 顶部解析 YAML frontmatter（--- 包裹），返回 map。
func (p *WikiPage) ParsedFrontmatter() map[string]any {
	return parseFrontmatter(p.Content)
}

// parseFrontmatter 解析 content 顶部的 YAML frontmatter（--- 包裹）。
func parseFrontmatter(content string) map[string]any {
	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, "---") {
		return map[string]any{}
	}
	lines := strings.Split(trimmed, "\n")
	var fm []string
	for i, line := range lines {
		if i == 0 {
			continue // 跳过首个 ---
		}
		if strings.TrimSpace(line) == "---" {
			break
		}
		fm = append(fm, line)
	}
	out := map[string]any{}
	if err := yaml.Unmarshal([]byte(strings.Join(fm, "\n")), &out); err != nil {
		return map[string]any{}
	}
	return out
}

// ListPagesResp 列表响应
//
// ⚠️ WeKnora 0.7.2 wiki/pages 端点实际返回 `pages` 字段（不是 `data`）。
// 之前用 `data` 解码导致 ListByVideo 返回空，进而让 AfterSkillComplete
// 找不到已存在的 wiki 页，5 个 skill job 全部误报「未找到 wiki 页」。
// 同时保留 `data` 字段以兼容其他端点/旧版响应。
type ListPagesResp struct {
	Pages      []WikiPage `json:"pages"`
	Data       []WikiPage `json:"data"`
	Total      int        `json:"total"`
	Page       int        `json:"page"`
	PageSize   int        `json:"page_size"`
	TotalPages int        `json:"total_pages"`
}

// AllPages 统一访问实际拿到的页列表（优先 pages，回退 data）
func (r *ListPagesResp) AllPages() []WikiPage {
	if len(r.Pages) > 0 {
		return r.Pages
	}
	return r.Data
}

// ListPages 列出 KB 内 Wiki 页面（按 page_type 可选过滤）
func (w *WikiClient) ListPages(ctx context.Context, kbID, pageType string) (*ListPagesResp, error) {
	return w.listPages(ctx, kbID, pageType, 1, 20)
}

func (w *WikiClient) listPages(ctx context.Context, kbID, pageType string, page, pageSize int) (*ListPagesResp, error) {
	u := fmt.Sprintf("%s/api/v1/knowledgebase/%s/wiki/pages", w.cfg.BaseURL, kbID)
	query := url.Values{}
	if pageType != "" {
		query.Set("page_type", pageType)
	}
	query.Set("page", strconv.Itoa(page))
	query.Set("page_size", strconv.Itoa(pageSize))
	u += "?" + query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	w.setHeaders(req)
	resp, err := w.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list wiki pages: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		buf, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("list wiki status %d: %s", resp.StatusCode, string(buf))
	}
	var out ListPagesResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetPageByID reads a Wiki page by its UUID. The public Wiki route is slug-based,
// so resolve the UUID through the paginated list endpoint before fetching the
// full page content.
func (w *WikiClient) GetPageByID(ctx context.Context, kbID, pageID string) (*WikiPage, error) {
	const pageSize = maxWikiPageSize
	for page := 1; ; page++ {
		resp, err := w.listPages(ctx, kbID, "", page, pageSize)
		if err != nil {
			return nil, fmt.Errorf("find wiki page %s: %w", pageID, err)
		}
		pages := resp.AllPages()
		for _, candidate := range pages {
			if candidate.ID == pageID {
				return w.GetPage(ctx, kbID, candidate.Slug)
			}
		}
		if len(pages) == 0 ||
			(resp.TotalPages > 0 && page >= resp.TotalPages) ||
			(resp.TotalPages == 0 && resp.Total > 0 && page*pageSize >= resp.Total) ||
			(resp.TotalPages == 0 && resp.Total == 0 && len(pages) < pageSize) {
			return nil, nil
		}
	}
}

// GetPage 读单个 Wiki 页面
func (w *WikiClient) GetPage(ctx context.Context, kbID, slug string) (*WikiPage, error) {
	u := fmt.Sprintf("%s/api/v1/knowledgebase/%s/wiki/pages/%s", w.cfg.BaseURL, kbID, url.PathEscape(slug))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	w.setHeaders(req)
	resp, err := w.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get wiki page: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode >= 400 {
		buf, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get wiki status %d: %s", resp.StatusCode, string(buf))
	}
	var out WikiPage
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (w *WikiClient) UpsertPage(ctx context.Context, kbID string, input WikiPageWrite) (*WikiPage, error) {
	if strings.TrimSpace(kbID) == "" {
		kbID = w.cfg.KBID
	}
	if strings.TrimSpace(kbID) == "" || strings.TrimSpace(input.Slug) == "" || strings.TrimSpace(input.Content) == "" {
		return nil, fmt.Errorf("wiki page kb_id, slug and content are required")
	}
	existing, err := w.GetPage(ctx, kbID, input.Slug)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		body, err := json.Marshal(map[string]any{
			"slug":      input.Slug,
			"title":     input.Title,
			"page_type": input.PageType,
			"status":    input.Status,
			"content":   input.Content,
			"summary":   input.Summary,
		})
		if err != nil {
			return nil, fmt.Errorf("encode wiki page: %w", err)
		}
		return w.writePage(ctx, http.MethodPost, fmt.Sprintf("%s/api/v1/knowledgebase/%s/wiki/pages", w.cfg.BaseURL, kbID), body, http.StatusCreated)
	}
	if input.Version == 0 {
		input.Version = existing.Version
	}

	body, err := json.Marshal(map[string]any{
		"title":     input.Title,
		"page_type": input.PageType,
		"status":    input.Status,
		"content":   input.Content,
		"summary":   input.Summary,
		"version":   input.Version,
	})
	if err != nil {
		return nil, fmt.Errorf("encode wiki page update: %w", err)
	}
	if input.Version == 0 {
		body, err = json.Marshal(map[string]any{
			"title":     input.Title,
			"page_type": input.PageType,
			"status":    input.Status,
			"content":   input.Content,
			"summary":   input.Summary,
		})
		if err != nil {
			return nil, fmt.Errorf("encode wiki page update: %w", err)
		}
	}
	return w.writePage(ctx, http.MethodPut, fmt.Sprintf("%s/api/v1/knowledgebase/%s/wiki/pages/%s", w.cfg.BaseURL, kbID, url.PathEscape(input.Slug)), body, http.StatusOK)
}

func (w *WikiClient) writePage(ctx context.Context, method, endpoint string, body []byte, expectedStatus int) (*WikiPage, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	w.setHeaders(req)
	req.Header.Set("Content-Type", "application/json")
	resp, err := w.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("write wiki page: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != expectedStatus {
		responseBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("write wiki page status %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	var page WikiPage
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		return nil, fmt.Errorf("decode wiki page: %w", err)
	}
	if page.ID == "" {
		return nil, fmt.Errorf("write wiki page returned empty id")
	}
	return &page, nil
}

// ListByVideo 列出某视频的关联 Wiki 页
//
// 匹配优先级：frontmatter.source_video_id > content 包含 videoID。
// 部分 skill（如 summary）不写 YAML frontmatter，回退到 content 包含匹配。
// pageType 为空时查所有 page_type；非空时加 API 过减少传输量。
func (w *WikiClient) ListByVideo(ctx context.Context, kbID, videoID string, pageType string) ([]WikiPage, error) {
	const pageSize = 100
	out := make([]WikiPage, 0)
	for page := 1; ; page++ {
		resp, err := w.listPages(ctx, kbID, pageType, page, pageSize)
		if err != nil {
			return nil, err
		}
		for _, p := range resp.AllPages() {
			if vid, _ := p.ParsedFrontmatter()["source_video_id"].(string); vid == videoID || strings.Contains(p.Content, videoID) {
				out = append(out, p)
			}
		}
		if len(resp.AllPages()) == 0 ||
			(resp.TotalPages > 0 && page >= resp.TotalPages) ||
			(resp.TotalPages == 0 && len(resp.AllPages()) < pageSize) {
			break
		}
	}
	return out, nil
}

const maxWikiPageSize = 100

func (w *WikiClient) setHeaders(req *http.Request) {
	if w.cfg.APIKey != "" {
		req.Header.Set("X-API-Key", w.cfg.APIKey)
	}
	if w.cfg.TenantID != "" {
		req.Header.Set("X-Tenant-ID", w.cfg.TenantID)
	}
}
