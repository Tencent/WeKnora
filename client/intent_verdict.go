package client

// IntentGate verdict 语料导出（T61，issue #24）的 SDK 支持。服务端 schema
// 见 internal/handler/intent_verdict.go 的 VerdictCorpusEntry——两处字段
// 必须逐字对齐（CLI 的 `--format json` 直接透传本结构）。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// VerdictCorpusEntry 是导出文件的一行：设计「(坏调用, 正确调用, 理由)
// 三元组」的落盘形态。prompt=会话首条用户消息；tool_call=原始调用
// （敏感工具只导 digest，红acted 形态）；verdict/human_override/
// judge_model 来自 intent_verdicts 行。
type VerdictCorpusEntry struct {
	VerdictID      string          `json:"verdict_id"`
	SessionID      string          `json:"session_id"`
	ToolCallID     string          `json:"tool_call_id"`
	Prompt         string          `json:"prompt"`
	ToolCall       json.RawMessage `json:"tool_call"`
	Verdict        string          `json:"verdict"`
	Reason         string          `json:"reason"`
	Layer          string          `json:"layer"`
	PolicyID       string          `json:"policy_id,omitempty"`
	PolicyVersion  int             `json:"policy_version,omitempty"`
	ModeAtDecision string          `json:"mode_at_decision"`
	HumanOverride  string          `json:"human_override"`
	JudgeModel     string          `json:"judge_model"`
	CreatedAt      time.Time       `json:"created_at"`
}

// VerdictCorpusExport 是 /intent-verdicts/export 的 data 载荷。
type VerdictCorpusExport struct {
	JudgeModel string                `json:"judge_model"`
	Count      int                   `json:"count"`
	Entries    []*VerdictCorpusEntry `json:"entries"`
}

// ExportVerdictCorpus 按 judge 模型分层导出判定语料。judgeModel 为空串
// 时导出规则层/baseline 判定（无 judge 模型归属的行）；limit<=0 用服务
// 端默认（1000）。
func (c *Client) ExportVerdictCorpus(ctx context.Context, judgeModel string, limit int) (*VerdictCorpusExport, error) {
	query := url.Values{}
	query.Set("judge_model", judgeModel)
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	resp, err := c.doRequest(ctx, http.MethodGet, "/api/v1/intent-verdicts/export", nil, query)
	if err != nil {
		return nil, err
	}

	var result struct {
		Success bool                 `json:"success"`
		Data    *VerdictCorpusExport `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	if result.Data == nil {
		return nil, fmt.Errorf("export verdict corpus: empty data")
	}
	return result.Data, nil
}
