package types

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// intent_verdicts 表（设计文档 §6.2）是 IntentGate 的判定日志与数据飞轮
// 数据源：每一行是一次工具调用的 Verdict 落库形态。术语见 CONTEXT.md
// （IntentGate / Verdict / Observe / Enforce）。
//
// 本文件的枚举常量与 internal/agent/intentgate 的 Action/Layer 取值保持
// 一致（types 不能反向 import intentgate，避免循环依赖）；值必须逐字相同，
// 保证日志可读、落库可对账。

// Verdict 判定的 layer 取值（设计 §6.2 layer 枚举）。
const (
	// VerdictLayerRule ① 确定性规则层。
	VerdictLayerRule = "rule"
	// VerdictLayerJudge ② 语义层（LLM judge）。
	VerdictLayerJudge = "judge"
	// VerdictLayerBaseline 无策略命中时的基线扫描（policy_id 为 NULL）。
	VerdictLayerBaseline = "baseline"
)

// Verdict 判定的 action 取值（设计 §6.2 verdict 枚举）。
const (
	VerdictActionAllow           = "allow"
	VerdictActionDeny            = "deny"
	VerdictActionRequireApproval = "require_approval"
	VerdictActionUncertain       = "uncertain"
)

// IntentPolicy 的 mode 取值（设计 §6.1 mode 枚举）。
const (
	// VerdictModeObserve 只记录不拦截，新策略一律 Observe 起步。
	VerdictModeObserve = "observe"
	// VerdictModeEnforce 按 Verdict 实际阻断或转人工审批。
	VerdictModeEnforce = "enforce"
)

// human_override 取值（设计 §6.2）：人工后续动作，飞轮关键字段。
const (
	HumanOverrideNone     = "none"
	HumanOverrideApproved = "approved"
	HumanOverrideModified = "modified"
	HumanOverrideRejected = "rejected"
)

// SensitiveArgsTools 与 internal/agent/act.go 的 toolHintSensitiveArgs
// 同款清单：这些工具的原始参数（如 database_query 的 SQL 原文）绝不落库，
// args 一律只存 sha256 digest（设计 §6.2 args_digest 列注）。repository 的
// 脱敏测试以此清单为准，防止两处清单漂移。
var SensitiveArgsTools = map[string]bool{
	"database_query": true,
}

// VerdictRecord 是 intent_verdicts 表的一行：一次工具调用的判定记录。
// 所有 id 字段来自现有 span metadata，可回链 Langfuse trace。
type VerdictRecord struct {
	ID                 string `json:"id"                  gorm:"type:varchar(36);primaryKey"`
	TenantID           uint64 `json:"tenant_id"           gorm:"column:tenant_id;not null;index:idx_intent_verdicts_session,priority:1;index:idx_intent_verdicts_policy,priority:1"`
	SessionID          string `json:"session_id"          gorm:"column:session_id;type:varchar(36);not null;index:idx_intent_verdicts_session,priority:2"`
	AssistantMessageID string `json:"assistant_message_id" gorm:"column:assistant_message_id;type:varchar(36);not null;default:''"`
	ToolCallID         string `json:"tool_call_id"        gorm:"column:tool_call_id;type:varchar(128);not null;default:''"`
	// PolicyID 为空（NULL）= 兜底判定（无策略命中时的基线扫描）。
	PolicyID      *string `json:"policy_id,omitempty"      gorm:"column:policy_id;type:varchar(36);index:idx_intent_verdicts_policy,priority:2"`
	PolicyVersion *int    `json:"policy_version,omitempty" gorm:"column:policy_version"`
	ToolName      string  `json:"tool_name"                gorm:"column:tool_name;type:varchar(512);not null"`
	// ArgsDigest 是工具参数的 sha256 hex（规范化 JSON 后计算）。原始参数
	// 永不落库——敏感参数（SQL 类）只存 digest，见 SensitiveArgsTools。
	ArgsDigest string `json:"args_digest" gorm:"column:args_digest;type:varchar(64);not null;default:''"`
	Layer      string `json:"layer"       gorm:"column:layer;type:varchar(16);not null"`
	Verdict    string `json:"verdict"     gorm:"column:verdict;type:varchar(32);not null"`
	// Reason 是 judge 的自然语言理由 / 命中的规则 ID。
	Reason         string `json:"reason"           gorm:"column:reason;type:text;not null;default:''"`
	ModeAtDecision string `json:"mode_at_decision" gorm:"column:mode_at_decision;type:varchar(16);not null;default:'observe'"`
	// LatencyMs / JudgeTokens 是成本观测字段（设计 §10 指标）。
	LatencyMs   int `json:"latency_ms"   gorm:"column:latency_ms;not null;default:0"`
	JudgeTokens int `json:"judge_tokens" gorm:"column:judge_tokens;not null;default:0"`
	// HumanOverride 记录人工后续动作（审批改参数等），飞轮关键字段。
	HumanOverride string    `json:"human_override" gorm:"column:human_override;type:varchar(16);not null;default:'none'"`
	// JudgeModel 是判定时使用的 judge 模型 ID（models.id；规则层/baseline
	// 判定为空串）。T61（issue #24）语料导出按此分层过滤——设计 §12 决策 2：
	// 杂牌 judge 产生的 verdict 语料质量方差大，蒸馏前按 judge 模型过滤。
	JudgeModel    string    `json:"judge_model"    gorm:"column:judge_model;type:varchar(64);not null;default:''"`
	CreatedAt     time.Time `json:"created_at"     gorm:"column:created_at;not null;index:idx_intent_verdicts_session,priority:3;index:idx_intent_verdicts_policy,priority:3"`
}

// TableName implements gorm's tabler.
func (VerdictRecord) TableName() string { return "intent_verdicts" }

// VerdictRecordInput 是 NewVerdictRecord 的入参。Args 是原始参数，
// 只用于计算 digest，绝不进入 VerdictRecord。
type VerdictRecordInput struct {
	TenantID           uint64
	SessionID          string
	AssistantMessageID string
	ToolCallID         string
	PolicyID           string // 空 = 兜底判定（layer=baseline）
	PolicyVersion      int    // <=0 = 无策略版本
	ToolName           string
	Args               json.RawMessage
	Layer              string
	Verdict            string
	Reason             string
	ModeAtDecision     string // 空 = observe
	LatencyMs          int
	JudgeTokens        int
	// JudgeModel 是判定时使用的 judge 模型 ID（LLMJudge 从解析出的模型
	// 元数据填入；规则层/baseline 判定为空）。落库为 judge_model 列
	// （T61 语料导出按此分层过滤，issue #24）。
	JudgeModel string
}

// ValidVerdictLayer 报告 layer 是否是合法枚举值。
func ValidVerdictLayer(layer string) bool {
	switch layer {
	case VerdictLayerRule, VerdictLayerJudge, VerdictLayerBaseline:
		return true
	}
	return false
}

// ValidVerdictAction 报告 verdict 是否是合法枚举值。
func ValidVerdictAction(verdict string) bool {
	switch verdict {
	case VerdictActionAllow, VerdictActionDeny, VerdictActionRequireApproval, VerdictActionUncertain:
		return true
	}
	return false
}

// ValidVerdictMode 报告 mode 是否是合法枚举值。
func ValidVerdictMode(mode string) bool {
	switch mode {
	case VerdictModeObserve, VerdictModeEnforce:
		return true
	}
	return false
}

// ValidHumanOverride 报告 human_override 是否是合法枚举值。
func ValidHumanOverride(override string) bool {
	switch override {
	case HumanOverrideNone, HumanOverrideApproved, HumanOverrideModified, HumanOverrideRejected:
		return true
	}
	return false
}

// DigestArgs 计算工具参数的 sha256 hex digest。计算前先把 args 规范化为
// 键序稳定的 JSON（Go map marshal 按键排序），使同一逻辑参数产生同一
// digest（供 §11 的 (policy_id, args_digest) 判定缓存命中）。空 args 返回
// 空串；非法 JSON 直接对原始字节取 digest，不因参数畸形阻断判定链路。
func DigestArgs(args json.RawMessage) string {
	if len(args) == 0 {
		return ""
	}
	canonical := args
	var v any
	if err := json.Unmarshal(args, &v); err == nil {
		if b, err := json.Marshal(v); err == nil {
			canonical = b
		}
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}

// NewVerdictRecord 由判定输入构造一行 verdict 记录。三个保证：
//  1. 枚举字段（layer/verdict/mode）逐一校验，拒绝脏数据静默进入 verdict
//     链路（与 intentgate.Action.UnmarshalJSON 同款取向）；
//  2. 原始参数只用于计算 ArgsDigest，记录上没有任何字段持有原文——
//     敏感参数（SQL 类）因此天然"不存原文只存 digest"；
//  3. PolicyID 为空时落 NULL（兜底判定，设计 §6.2）。
func NewVerdictRecord(in VerdictRecordInput) (*VerdictRecord, error) {
	if !ValidVerdictLayer(in.Layer) {
		return nil, fmt.Errorf("intentgate: unknown verdict layer %q", in.Layer)
	}
	if !ValidVerdictAction(in.Verdict) {
		return nil, fmt.Errorf("intentgate: unknown verdict action %q", in.Verdict)
	}
	mode := in.ModeAtDecision
	if mode == "" {
		mode = VerdictModeObserve
	}
	if !ValidVerdictMode(mode) {
		return nil, fmt.Errorf("intentgate: unknown policy mode %q", in.ModeAtDecision)
	}
	rec := &VerdictRecord{
		ID:                 uuid.NewString(),
		TenantID:           in.TenantID,
		SessionID:          in.SessionID,
		AssistantMessageID: in.AssistantMessageID,
		ToolCallID:         in.ToolCallID,
		ToolName:           in.ToolName,
		ArgsDigest:         DigestArgs(in.Args),
		Layer:              in.Layer,
		Verdict:            in.Verdict,
		Reason:             in.Reason,
		ModeAtDecision:     mode,
		LatencyMs:          in.LatencyMs,
		JudgeTokens:        in.JudgeTokens,
		JudgeModel:         in.JudgeModel,
		HumanOverride:      HumanOverrideNone,
		CreatedAt:          time.Now().UTC(),
	}
	if in.PolicyID != "" {
		policyID := in.PolicyID
		rec.PolicyID = &policyID
		if in.PolicyVersion > 0 {
			version := in.PolicyVersion
			rec.PolicyVersion = &version
		}
	}
	return rec, nil
}
