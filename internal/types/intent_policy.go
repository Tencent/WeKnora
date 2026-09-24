package types

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// intent_policies 表（设计文档 §6.1）是 IntentGate 的策略表：IntentPolicy
// 是版本化的配置资产，绑定 scope（tool/service/agent/workspace/tenant），
// 含自然语言约束（NLC）原文、可编译规则表达式与 mode。按租户隔离。
// 术语见 CONTEXT.md（IntentPolicy / 自然语言约束 / Observe / Enforce）。

// IntentPolicy 的 scope_type 取值（设计 §6.1 scope 枚举）。scope 解析顺序
// tool > service > agent > workspace > tenant（设计 §8.3）。
const (
	// PolicyScopeTool 单个工具；scope_ref 为 `service_id:tool_name`，
	// 支持 `*:wiki_*` 前缀通配。
	PolicyScopeTool = "tool"
	// PolicyScopeService 整个 MCP 服务。
	PolicyScopeService = "service"
	// PolicyScopeAgent 单个 agent。
	PolicyScopeAgent = "agent"
	// PolicyScopeWorkspace 整个 workspace。
	PolicyScopeWorkspace = "workspace"
	// PolicyScopeTenant 整个租户（兜底级）。
	PolicyScopeTenant = "tenant"
)

// IntentPolicy 的 risk_tier 取值（设计 §6.1）。high = 不确定时也强制进
// 语义层 / 失败时 fail-close（设计 §8.1、§9）。
const (
	RiskTierLow  = "low"
	RiskTierHigh = "high"
)

// IntentPolicy 的 mode 取值复用 VerdictModeObserve / VerdictModeEnforce
// （定义在 intent_verdict.go，枚举值逐字一致，保证落库可对账）。

// ValidPolicyScope 报告 scope_type 是否是合法枚举值。
func ValidPolicyScope(scope string) bool {
	switch scope {
	case PolicyScopeTool, PolicyScopeService, PolicyScopeAgent, PolicyScopeWorkspace, PolicyScopeTenant:
		return true
	}
	return false
}

// ValidRiskTier 报告 risk_tier 是否是合法枚举值。
func ValidRiskTier(tier string) bool {
	switch tier {
	case RiskTierLow, RiskTierHigh:
		return true
	}
	return false
}

// IntentPolicy 是 intent_policies 表的一行：一条版本化的意图策略。
// 每次修改产生新行（version+1，旧版本保留，设计 §3.2）；同一策略谱系
// 由 (tenant_id, scope_type, scope_ref) 标识，version 单调递增。
type IntentPolicy struct {
	ID       string `json:"id"        gorm:"type:varchar(36);primaryKey"`
	TenantID uint64 `json:"tenant_id" gorm:"column:tenant_id;not null;index:idx_intent_policies_scope,priority:1"`
	// ScopeType / ScopeRef 决定这条策略管谁（设计 §6.1）。
	ScopeType string `json:"scope_type" gorm:"column:scope_type;type:varchar(16);not null;index:idx_intent_policies_scope,priority:2"`
	ScopeRef  string `json:"scope_ref"  gorm:"column:scope_ref;type:varchar(512);not null;default:'';index:idx_intent_policies_scope,priority:3"`
	// ArgPath 是参数路径表达式（如 `$.amount`）；NULL = 整条调用。
	ArgPath *string `json:"arg_path,omitempty" gorm:"column:arg_path;type:varchar(256)"`
	// ConstraintText 是 NLC 原文（"单笔退款不得超过 $75"）。
	ConstraintText string `json:"constraint_text" gorm:"column:constraint_text;type:text;not null;default:''"`
	// RuleExpr 是可编译的确定性表达式（`value <= 75`）；NULL = 走语义层
	// judge（设计 §6.1、§8.1）。
	RuleExpr *string `json:"rule_expr,omitempty" gorm:"column:rule_expr;type:text"`
	RiskTier string  `json:"risk_tier"         gorm:"column:risk_tier;type:varchar(8);not null;default:'low'"`
	// Mode 默认 observe：只记录不拦截（设计 §9）。
	Mode      string    `json:"mode"    gorm:"column:mode;type:varchar(16);not null;default:'observe'"`
	Version   int       `json:"version" gorm:"column:version;not null;default:1"`
	Enabled   bool      `json:"enabled" gorm:"column:enabled;not null;default:true"`
	CreatedBy string    `json:"created_by" gorm:"column:created_by;type:varchar(36);not null;default:''"`
	CreatedAt time.Time `json:"created_at" gorm:"column:created_at;not null"`
	UpdatedAt time.Time `json:"updated_at" gorm:"column:updated_at;not null"`
}

// TableName implements gorm's tabler.
func (IntentPolicy) TableName() string { return "intent_policies" }

// IntentPolicyInput 是 NewIntentPolicy 的入参。
type IntentPolicyInput struct {
	TenantID       uint64
	ScopeType      string
	ScopeRef       string
	ArgPath        string // 空 = NULL（整条调用）
	ConstraintText string
	RuleExpr       string // 空 = NULL（走语义层）
	RiskTier       string // 空 = low
	Mode           string // 空 = observe（新策略一律 Observe 起步）
	Version        int    // 0 = 1；<0 拒绝
	CreatedBy      string
}

// NewIntentPolicy 构造一条新策略（或某谱系的新版本）。保证：
//  1. 枚举字段（scope_type/risk_tier/mode）逐一校验，拒绝脏数据；
//  2. tool/service/agent/workspace 级必须有 scope_ref，tenant 级允许为空；
//  3. NLC 原文必填——策略是要持续运营的资产，没有原文无法评审与回灌
//     （设计 §3.2）；
//  4. 缺省值对齐设计 §6.1：risk_tier=low、mode=observe、version 从 1 起、
//     enabled=true。
func NewIntentPolicy(in IntentPolicyInput) (*IntentPolicy, error) {
	if !ValidPolicyScope(in.ScopeType) {
		return nil, fmt.Errorf("intentgate: unknown policy scope_type %q", in.ScopeType)
	}
	if in.ScopeType != PolicyScopeTenant && in.ScopeRef == "" {
		return nil, fmt.Errorf("intentgate: scope_ref is required for scope_type %q", in.ScopeType)
	}
	if in.ConstraintText == "" {
		return nil, fmt.Errorf("intentgate: constraint_text is required")
	}
	riskTier := in.RiskTier
	if riskTier == "" {
		riskTier = RiskTierLow
	}
	if !ValidRiskTier(riskTier) {
		return nil, fmt.Errorf("intentgate: unknown risk_tier %q", in.RiskTier)
	}
	mode := in.Mode
	if mode == "" {
		mode = VerdictModeObserve
	}
	if !ValidVerdictMode(mode) {
		return nil, fmt.Errorf("intentgate: unknown policy mode %q", in.Mode)
	}
	if in.Version < 0 {
		return nil, fmt.Errorf("intentgate: version must be >= 0, got %d", in.Version)
	}
	version := in.Version
	if version == 0 {
		version = 1
	}
	now := time.Now().UTC()
	p := &IntentPolicy{
		ID:             uuid.NewString(),
		TenantID:       in.TenantID,
		ScopeType:      in.ScopeType,
		ScopeRef:       in.ScopeRef,
		ConstraintText: in.ConstraintText,
		RiskTier:       riskTier,
		Mode:           mode,
		Version:        version,
		Enabled:        true,
		CreatedBy:      in.CreatedBy,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if in.ArgPath != "" {
		argPath := in.ArgPath
		p.ArgPath = &argPath
	}
	if in.RuleExpr != "" {
		ruleExpr := in.RuleExpr
		p.RuleExpr = &ruleExpr
	}
	return p, nil
}
