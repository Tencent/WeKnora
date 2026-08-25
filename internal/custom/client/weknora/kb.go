// Package weknora KB 配置 API 封装（CP-T010）。
//
// 端点（WeKnora 0.7.2 handler/knowledgebase.go）：
//   - PUT /api/v1/knowledgebase/{kb_id}
//
// 设计要点：
//   - 文档入 KB 后 WeKnora 自动抽取 Wiki 页（entity/concept 原生输出，第一源）
//   - WikiEnabled = true 是抽取的总开关
//   - ExtractConfig 控制抽取的细粒度（passage_size / entity_types 等）
//   - 仅在 custom-backend 启动时调用一次（幂等）；不做运行期反复改写
package weknora

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Tencent/WeKnora/internal/custom/config"
)

// KBClient KB 配置客户端
type KBClient struct {
	cfg  config.WeKnoraConfig
	http *http.Client
}

// NewKBClient 构造
func NewKBClient(cfg config.WeKnoraConfig) *KBClient {
	return &KBClient{
		cfg:  cfg,
		http: &http.Client{Timeout: 30 * time.Second},
	}
}

// KBConfig WeKnora KB 配置子集（只覆盖 CP-T010 涉及的字段，避免过度耦合）
type KBConfig struct {
	WikiEnabled    bool                   `json:"wiki_enabled"`     // 抽取 Wiki 页（entity/concept）
	GraphEnabled   bool                   `json:"graph_enabled"`    // 跨视频图谱 Neo4j（CP-T010 后续可选）
	ExtractConfig  map[string]any         `json:"extract_config,omitempty"`
	ChunkConfig    map[string]any         `json:"chunk_config,omitempty"`
}

// UpdateKBConfig 调 PUT /api/v1/knowledgebase/{kb_id}，开启原生 Wiki 抽取
func (k *KBClient) UpdateKBConfig(ctx context.Context, kbID string, cfg KBConfig) error {
	if kbID == "" {
		return fmt.Errorf("kb_id empty")
	}
	body, err := json.Marshal(map[string]any{
		// 名称/描述沿用旧值（WeKnora 要求必填，此处补默认）
		"name":        "video-knowledge-base",
		"description": "字幕分块知识库（自研后端配置）",
		"config":      cfg,
	})
	if err != nil {
		return err
	}
	u := fmt.Sprintf("%s/api/v1/knowledge-bases/%s", k.cfg.BaseURL, kbID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	k.setHeaders(req)
	resp, err := k.http.Do(req)
	if err != nil {
		return fmt.Errorf("update kb config: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		buf, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("update kb status %d: %s", resp.StatusCode, string(buf))
	}
	return nil
}

// EnableWikiExtraction 一键开启原生 Wiki 抽取（CP-T010）
func (k *KBClient) EnableWikiExtraction(ctx context.Context, kbID string) error {
	return k.UpdateKBConfig(ctx, kbID, KBConfig{
		WikiEnabled: true,
		// 抽取粒度：单句级（与自研分块粒度对齐）
		ExtractConfig: map[string]any{
			"passage_size":    500,
			"extract_types":   []string{"entity", "concept"},
			"language":        "auto",
		},
	})
}

func (k *KBClient) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	if k.cfg.APIKey != "" {
		req.Header.Set("X-API-Key", k.cfg.APIKey)
	}
	if k.cfg.TenantID != "" {
		req.Header.Set("X-Tenant-ID", k.cfg.TenantID)
	}
}