// IntentGate 判定报表接口（T51，issue #22）：每策略的 verdict 分布与
// 单条下钻。只读、Admin+（与策略管理同门槛：verdict 理由可能含业务
// 上下文，不对普通成员开放）。
package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// VerdictCorpusEntry 是 T61（issue #24）导出文件的一行：设计 §11 飞轮
// 「(坏调用, 正确调用, 理由) 三元组」的落盘形态。prompt/tool_call 不在
// verdict 表（参数只存 digest），导出时从会话消息回填；回填失败不阻断
// 该行导出（prompt 落空串、tool_call 落 null），语料 consumer 自行过滤。
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

type IntentVerdictHandler struct {
	repo        interfaces.IntentVerdictRepository
	messageRepo interfaces.MessageRepository
}

func NewIntentVerdictHandler(repo interfaces.IntentVerdictRepository, messageRepo interfaces.MessageRepository) *IntentVerdictHandler {
	return &IntentVerdictHandler{repo: repo, messageRepo: messageRepo}
}

func (h *IntentVerdictHandler) tenantID(c *gin.Context) uint64 {
	return c.GetUint64(types.TenantIDContextKey.String())
}

// ListVerdicts godoc
// @Summary      判定记录列表
// @Description  按策略（可选，空=全部含 baseline）列出 verdict 记录，created_at 倒序。
// @Tags         IntentGate 判定
// @Produce      json
// @Param        policy_id  query     string  false  "策略ID（空=全部）"
// @Param        limit      query     int     false  "条数上限，默认 100"
// @Success      200  {object}  map[string]interface{}  "记录列表"
// @Security     Bearer
// @Router       /intent-verdicts [get]
func (h *IntentVerdictHandler) ListVerdicts(c *gin.Context) {
	tenantID := h.tenantID(c)
	policyID := c.Query("policy_id")
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))

	var (
		rows []*types.VerdictRecord
		err  error
	)
	if policyID != "" {
		rows, err = h.repo.ListByPolicy(c.Request.Context(), tenantID, policyID, limit)
	} else {
		rows, err = h.repo.ListByTenant(c.Request.Context(), tenantID, limit)
	}
	if err != nil {
		respondPolicyError(c, err, "list verdicts")
		return
	}
	if rows == nil {
		rows = []*types.VerdictRecord{}
	}
	// 报表消费倒序（最新在前），与 verdict 表的 created_at DESC 习惯一致。
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": rows})
}

// VerdictSummary godoc
// @Summary      verdict 分布汇总
// @Description  按 verdict 取值计数（T51 报表页分布数字），policy_id 可选。
// @Tags         IntentGate 判定
// @Produce      json
// @Param        policy_id  query     string  false  "策略ID（空=全部）"
// @Success      200  {object}  map[string]interface{}  "{counts: {allow: n, ...}, total: n}"
// @Security     Bearer
// @Router       /intent-verdicts/summary [get]
func (h *IntentVerdictHandler) VerdictSummary(c *gin.Context) {
	tenantID := h.tenantID(c)
	counts, err := h.repo.CountByVerdictGrouped(c.Request.Context(), tenantID, c.Query("policy_id"))
	if err != nil {
		respondPolicyError(c, err, "summarize verdicts")
		return
	}
	var total int64
	for _, n := range counts {
		total += n
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"counts": counts, "total": total}})
}

// ExportCorpus godoc
// @Summary      verdict 语料导出（T61，issue #24）
// @Description  按 judge 模型分层导出判定语料（坏调用/正确调用/理由三元组）。
// @Description  prompt=会话首条用户消息，tool_call=原始调用（敏感工具只导 digest），
// @Description  附带 verdict/reason/human_override/judge_model。只读、Admin+。
// @Tags         IntentGate 判定
// @Produce      json
// @Param        judge_model  query     string  false  "judge 模型 ID 过滤（空串=规则层/baseline 判定）"
// @Param        limit        query     int     false  "条数上限，默认 1000"
// @Success      200  {object}  map[string]interface{}  "{judge_model, count, entries}"
// @Security     Bearer
// @Router       /intent-verdicts/export [get]
func (h *IntentVerdictHandler) ExportCorpus(c *gin.Context) {
	tenantID := h.tenantID(c)
	judgeModel := c.Query("judge_model")
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "1000"))

	rows, err := h.repo.ListByJudgeModel(c.Request.Context(), tenantID, judgeModel, limit)
	if err != nil {
		respondPolicyError(c, err, "export verdict corpus")
		return
	}
	entries := make([]*VerdictCorpusEntry, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, h.corpusEntry(c, row))
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
		"judge_model": judgeModel,
		"count":       len(entries),
		"entries":     entries,
	}})
}

// corpusEntry 把一行 verdict 记录扩成导出条目：回填 prompt（会话首条用户
// 消息，与 judge 的意图基准同源的会话级锚点）与 tool_call 原文（assistant
// 消息 agent_steps 里按 tool_call_id 定位）。敏感工具（SensitiveArgsTools）
// 的参数原文与落库策略一致永不导出，只给 digest 标记。
func (h *IntentVerdictHandler) corpusEntry(c *gin.Context, row *types.VerdictRecord) *VerdictCorpusEntry {
	entry := &VerdictCorpusEntry{
		VerdictID:      row.ID,
		SessionID:      row.SessionID,
		ToolCallID:     row.ToolCallID,
		Verdict:        row.Verdict,
		Reason:         row.Reason,
		Layer:          row.Layer,
		ModeAtDecision: row.ModeAtDecision,
		HumanOverride:  row.HumanOverride,
		JudgeModel:     row.JudgeModel,
		CreatedAt:      row.CreatedAt,
	}
	if row.PolicyID != nil {
		entry.PolicyID = *row.PolicyID
	}
	if row.PolicyVersion != nil {
		entry.PolicyVersion = *row.PolicyVersion
	}
	ctx := c.Request.Context()
	if first, err := h.messageRepo.GetFirstMessageOfUser(ctx, row.SessionID); err == nil && first != nil {
		entry.Prompt = first.Content
	}
	entry.ToolCall = h.corpusToolCall(ctx, row)
	return entry
}

// corpusToolCall 定位原始工具调用并整形为 {"name": ..., "arguments": ...}。
// 找不到（消息未落库/ID 漂移）时返回 nil（JSON null）；敏感工具只导红acted
// 形态，与 intent_verdicts.args_digest「原文永不落库」的策略对齐。
func (h *IntentVerdictHandler) corpusToolCall(ctx context.Context, row *types.VerdictRecord) json.RawMessage {
	if row.ToolCallID == "" || row.AssistantMessageID == "" {
		return nil
	}
	msg, err := h.messageRepo.GetMessage(ctx, row.SessionID, row.AssistantMessageID)
	if err != nil || msg == nil {
		return nil
	}
	for _, step := range msg.AgentSteps {
		for _, tc := range step.ToolCalls {
			if tc.ID != row.ToolCallID {
				continue
			}
			name := tc.Name
			if row.ToolName != "" {
				name = row.ToolName // model 视角的调用名（含 mcp_ 直注册名）
			}
			if types.SensitiveArgsTools[name] {
				redacted, _ := json.Marshal(map[string]any{
					"name":        name,
					"arguments":   map[string]any{"_redacted": true},
					"args_digest": row.ArgsDigest,
				})
				return redacted
			}
			call, err := json.Marshal(map[string]any{"name": name, "arguments": tc.Args})
			if err != nil {
				return nil
			}
			return call
		}
	}
	return nil
}
