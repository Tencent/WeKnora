// Package weknora Agent Chat API 封装（CP-T003）。
//
// 用途：触发 WeKnora 自定义 agent 执行指定 skill（spec §4.2）。
//
// 端点（来自 WeKnora 0.7.2 handler/session/handler.go + qa.go）：
//   - POST /api/v1/sessions                         创建会话
//   - POST /api/v1/agent-chat/{session_id}          触发 agent（含 skill_names）
//
// 设计要点：
//   - 每次 skill 触发独立 session（避免污染）
//   - SSE 流式响应只关心「完成事件」，本版本取首末两端就够
//   - 触发返回后置入 video_processing_jobs；产物 ID 由 agent 通过工具写回 Wiki 后
//     自研后端再读 Wiki 列表（CP-T008/009）→ 回写 videos 表
package weknora

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/custom/config"
)

// AgentClient skill 触发专用客户端（CP-T003）
type AgentClient struct {
	cfg  config.WeKnoraConfig
	http *http.Client
}

// NewAgentClient 构造
func NewAgentClient(cfg config.WeKnoraConfig) *AgentClient {
	return &AgentClient{
		cfg:  cfg,
		http: &http.Client{Timeout: 30 * time.Minute}, // skill 执行长
	}
}

// CreateSession 建会话（POST /api/v1/sessions）
func (a *AgentClient) CreateSession(ctx context.Context, title string) (string, error) {
	body, _ := json.Marshal(map[string]string{
		"title":       title,
		"description": "内容生产触发会话（自动创建）",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.cfg.BaseURL+"/api/v1/sessions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	a.setHeaders(req)
	resp, err := a.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("create session: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		buf, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("create session status %d: %s", resp.StatusCode, string(buf))
	}
	var out struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode session: %w", err)
	}
	if out.Data.ID == "" {
		return "", fmt.Errorf("create session 返回空 ID")
	}
	return out.Data.ID, nil
}

// TriggerSkill 触发单个 skill（Agent Chat API + skill_names）
func (a *AgentClient) TriggerSkill(ctx context.Context, sessionID, agentID, skillName, query string, knowledgeIDs []string) error {
	request := map[string]any{
		"query":         query,
		"agent_enabled": true,
		"agent_id":      agentID,
		"skill_names":   []string{skillName},
		"channel":       "content_pipeline",
		"disable_title": true,
		"knowledge_ids": knowledgeIDs,
	}
	if a.cfg.KBID != "" {
		request["knowledge_base_ids"] = []string{a.cfg.KBID}
	}
	body, _ := json.Marshal(request)
	url := fmt.Sprintf("%s/api/v1/agent-chat/%s", a.cfg.BaseURL, sessionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	a.setHeaders(req)
	// SSE 流式接收
	req.Header.Set("Accept", "text/event-stream")
	resp, err := a.http.Do(req)
	if err != nil {
		return fmt.Errorf("trigger skill %s: %w", skillName, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		buf, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("trigger skill status %d: %s", resp.StatusCode, string(buf))
	}
	// 消费 SSE：等到 [DONE] 或 error 事件
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	eventType := ""
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			eventType = ""
			continue
		}
		if strings.HasPrefix(line, "event:") {
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "[DONE]" {
				return nil
			}
			if eventType == "error" {
				return fmt.Errorf("agent chat error: %s", data)
			}
			// 检测 error 事件
			var evt map[string]any
			if err := json.Unmarshal([]byte(data), &evt); err == nil {
				responseType, _ := evt["response_type"].(string)
				done, _ := evt["done"].(bool)
				if responseType == "complete" && done {
					return nil
				}
				if responseType == "error" {
					return fmt.Errorf("agent chat error: %v", evt["content"])
				}
				if evtType, ok := evt["type"].(string); ok && (evtType == "error" || evtType == "ERROR") {
					return fmt.Errorf("agent chat error: %v", evt["message"])
				}
				if value, ok := evt["error"]; ok && value != nil {
					return fmt.Errorf("agent chat error: %v", value)
				}
			}
		}
	}
	return scanner.Err()
}

func (a *AgentClient) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	if a.cfg.APIKey != "" {
		req.Header.Set("X-API-Key", a.cfg.APIKey)
	}
	if a.cfg.TenantID != "" {
		req.Header.Set("X-Tenant-ID", a.cfg.TenantID)
	}
}
