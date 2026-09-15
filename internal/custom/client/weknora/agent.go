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
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/contentprovenance"
	"github.com/Tencent/WeKnora/internal/custom/config"
)

// AgentClient skill 触发专用客户端（CP-T003）
type AgentClient struct {
	cfg  config.WeKnoraConfig
	http *http.Client
}

// AgentToolResultSummary is deliberately limited to metadata. Tool output can
// contain transcript text or credentials and must never be persisted as a
// diagnostic artifact.
type AgentToolResultSummary struct {
	ToolName    string `json:"tool_name,omitempty"`
	Success     bool   `json:"success"`
	Error       string `json:"error,omitempty"`
	OutputBytes int    `json:"output_bytes,omitempty"`
}

// AgentRunDiagnostic is the safe, bounded observability contract for one
// Agent session. It records enough information to distinguish failure classes
// without storing prompts, model responses, or tool payloads.
type AgentRunDiagnostic struct {
	SessionID      string                   `json:"session_id"`
	SkillName      string                   `json:"skill_name"`
	TotalSteps     int                      `json:"total_steps,omitempty"`
	Rounds         int                      `json:"rounds,omitempty"`
	Outcome        string                   `json:"outcome,omitempty"`
	FailureClass   string                   `json:"failure_class,omitempty"`
	FailureCode    string                   `json:"failure_code,omitempty"`
	FailureMessage string                   `json:"failure_message,omitempty"`
	ModelErrors    []string                 `json:"model_errors,omitempty"`
	ToolErrors     []string                 `json:"tool_errors,omitempty"`
	ToolResults    []AgentToolResultSummary `json:"tool_results,omitempty"`
}

// AgentFailure carries the structured diagnostic while preserving the
// original error for existing retry and classification behavior.
type AgentFailure struct {
	Diagnostic AgentRunDiagnostic
	Err        error
}

func (e *AgentFailure) Error() string { return e.Err.Error() }
func (e *AgentFailure) Unwrap() error { return e.Err }

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
func (a *AgentClient) TriggerSkill(
	ctx context.Context,
	sessionID, agentID, skillName, query string,
	knowledgeIDs []string,
	productionJob *contentprovenance.Job,
) error {
	diagnostic := AgentRunDiagnostic{SessionID: sessionID, SkillName: skillName}
	fail := func(err error) error {
		diagnostic.FailureMessage = safeDiagnosticText(err.Error())
		diagnostic.FailureCode = diagnostic.FailureMessage
		diagnostic.FailureClass = classifyAgentFailure(diagnostic, err)
		return &AgentFailure{Diagnostic: diagnostic, Err: err}
	}
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
		return fail(err)
	}
	a.setHeaders(req)
	if productionJob != nil {
		knowledgeBaseIDs := []string(nil)
		if a.cfg.KBID != "" {
			knowledgeBaseIDs = []string{a.cfg.KBID}
		}
		envelope := contentprovenance.NewEnvelope(
			*productionJob, sessionID, agentID, skillName, query, knowledgeBaseIDs, knowledgeIDs,
		)
		encoded, signed, err := contentprovenance.Sign(a.cfg.ContentPipelineAuditSecret, envelope)
		if err != nil {
			return fail(fmt.Errorf("sign content pipeline provenance: %w", err))
		}
		req.Header.Set(contentprovenance.EnvelopeHeader, encoded)
		req.Header.Set(contentprovenance.SignatureHeader, signed)
	}
	// SSE 流式接收
	req.Header.Set("Accept", "text/event-stream")
	resp, err := a.http.Do(req)
	if err != nil {
		return fail(fmt.Errorf("trigger skill %s: %w", skillName, err))
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		buf, _ := io.ReadAll(resp.Body)
		return fail(fmt.Errorf("trigger skill status %d: %s", resp.StatusCode, safeDiagnosticText(string(buf))))
	}
	// 消费 SSE：只在完成事件或终止错误时结束。Agent 会把单次工具
	// 调用失败也作为 response_type=error 推送，但该事件 done=false，
	// 随后仍会继续推理和重试，不能据此提前启动新的处理任务。
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	eventType := ""
	productionGraph := productionJob != nil && strings.EqualFold(strings.TrimSpace(productionJob.JobType), "graph")
	sawAuditedWikiWrite := false
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
				if productionGraph {
					return fail(fmt.Errorf("agent chat production graph stream ended without a verified completion event"))
				}
				return nil
			}
			// 检测 complete / terminal error 事件。非终止工具错误留给
			// Agent 消化，继续读取同一条 SSE 流。
			var evt map[string]any
			if err := json.Unmarshal([]byte(data), &evt); err == nil {
				responseType, _ := evt["response_type"].(string)
				// WeKnora stream events use the top-level `type` field (for
				// example `complete`), while older gateways exposed
				// `response_type`. Accept both so terminal events cannot be
				// silently ignored and leave the producer connection hanging.
				if strings.TrimSpace(responseType) == "" {
					responseType, _ = evt["type"].(string)
				}
				done, _ := evt["done"].(bool)
				payload := eventPayload(evt)
				if round := intValue(payload["round"]); round > diagnostic.Rounds {
					diagnostic.Rounds = round
				}
				if iteration := intValue(payload["iteration"]); iteration > diagnostic.Rounds {
					diagnostic.Rounds = iteration
				}
				if responseType == "tool_result" {
					summary := AgentToolResultSummary{
						ToolName: safeDiagnosticText(stringValue(payload["tool_name"])),
						Success:  boolValue(payload["success"]),
						Error:    safeDiagnosticText(stringValue(payload["error"])),
					}
					if output := stringValue(payload["tool_output"]); output != "" {
						summary.OutputBytes = len(output)
					}
					if len(diagnostic.ToolResults) < 64 {
						diagnostic.ToolResults = append(diagnostic.ToolResults, summary)
					}
					if !summary.Success && summary.Error != "" {
						diagnostic.ToolErrors = appendBoundedDiagnostic(diagnostic.ToolErrors, summary.Error)
					}
				}
				if responseType == "tool_result" && isAuditedWikiWrite(evt) {
					sawAuditedWikiWrite = true
				}
				if responseType == "complete" && done {
					completion := payload
					outcome := strings.ToLower(strings.TrimSpace(stringValue(completion["outcome"])))
					diagnostic.Outcome = outcome
					diagnostic.TotalSteps = intValue(completion["total_steps"])
					if reason := safeDiagnosticText(completionFailureReason(completion)); reason != "agent_completion_failed" {
						diagnostic.ModelErrors = appendBoundedDiagnostic(diagnostic.ModelErrors, reason)
					}
					if outcome == "failed" || outcome == "error" || outcome == "canceled" || outcome == "cancelled" {
						reason := completionFailureReason(completion)
						return fail(fmt.Errorf("agent chat failed: %s", reason))
					}
					if productionGraph && intValue(completion["total_steps"]) <= 0 {
						return fail(fmt.Errorf("agent chat production graph completed without valid agent steps"))
					}
					if productionGraph && !sawAuditedWikiWrite {
						return fail(fmt.Errorf("agent chat production graph completed without an audited wiki_write_page"))
					}
					return nil
				}
				if responseType == "error" && done {
					err := fmt.Errorf("agent chat error: %v", evt["content"])
					diagnostic.ModelErrors = appendBoundedDiagnostic(diagnostic.ModelErrors, safeDiagnosticText(stringValue(evt["content"])))
					return fail(err)
				}
				if evtType, ok := evt["type"].(string); ok && done && (evtType == "error" || evtType == "ERROR") {
					err := fmt.Errorf("agent chat error: %v", evt["message"])
					diagnostic.ModelErrors = appendBoundedDiagnostic(diagnostic.ModelErrors, safeDiagnosticText(stringValue(evt["message"])))
					return fail(err)
				}
				if value, ok := evt["error"]; ok && value != nil && done {
					err := fmt.Errorf("agent chat error: %v", value)
					return fail(err)
				}
				continue
			}
			if eventType == "error" {
				return fail(fmt.Errorf("agent chat error: %s", data))
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fail(err)
	}
	return fail(fmt.Errorf("agent chat stream ended before a terminal event"))
}

func classifyAgentFailure(diagnostic AgentRunDiagnostic, err error) string {
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	if errors.Is(err, context.DeadlineExceeded) || strings.Contains(message, "timeout") || strings.Contains(message, "timed out") {
		return "model_timeout"
	}
	if containsAny(message, "context length", "context window", "maximum context", "too many tokens", "max tokens", "token limit") {
		return "context_limit"
	}
	for _, result := range diagnostic.ToolResults {
		if !result.Success {
			return "tool_failure"
		}
	}
	if containsAny(message, "production graph", "audited wiki_write_page", "audited_wiki_write", "missing_audited", "contract", "invalid json", "schema", "empty_response", "empty response", "invalid output", "output invalid", "stream ended", "without valid agent steps") {
		return "output_contract"
	}
	if strings.HasPrefix(message, "agent chat failed:") || len(diagnostic.ModelErrors) > 0 {
		return "model_error"
	}
	return "transport_error"
}

func safeDiagnosticText(value string) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if len(value) > 512 {
		return value[:512]
	}
	return value
}

func appendBoundedDiagnostic(values []string, value string) []string {
	value = safeDiagnosticText(value)
	if value == "" || len(values) >= 16 {
		return values
	}
	return append(values, value)
}

func containsAny(message string, fragments ...string) bool {
	for _, fragment := range fragments {
		if strings.Contains(message, fragment) {
			return true
		}
	}
	return false
}

func completionFailureReason(completion map[string]any) string {
	for _, key := range []string{"failure_reason", "error", "message", "content"} {
		if value := strings.TrimSpace(stringValue(completion[key])); value != "" {
			return value
		}
	}
	return "agent_completion_failed"
}

func stringValue(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

func boolValue(value any) bool {
	result, _ := value.(bool)
	return result
}

func isAuditedWikiWrite(evt map[string]any) bool {
	data := eventPayload(evt)
	toolName, _ := data["tool_name"].(string)
	success, _ := data["success"].(bool)
	productionSource, _ := data["production_source"].(map[string]any)
	producer, _ := productionSource["page_producer"].(string)
	eventID, _ := productionSource["event_id"].(string)
	return strings.TrimSpace(toolName) == "wiki_write_page" && success &&
		strings.TrimSpace(producer) == "extract_video_knowledge_v2" && strings.TrimSpace(eventID) != ""
}

func eventPayload(evt map[string]any) map[string]any {
	if data, ok := evt["data"].(map[string]any); ok {
		return data
	}
	return evt
}

func intValue(value any) int {
	if number, ok := value.(float64); ok {
		return int(number)
	}
	if number, ok := value.(int); ok {
		return number
	}
	return 0
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
