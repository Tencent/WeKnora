package router

import (
	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/handler"
)

// RegisterIntentPolicyRoutes registers the IntentGate policy CRUD routes
// (设计文档 §3.2 策略生命周期；issue #11)。
//
// Authorization: 策略是租户管理员运营的配置资产（设计 §3.2 参与者是
// 租户管理员），读写全部 Admin+。API key 仅 full-access 可调用——
// 策略原文（NLC）可能含业务敏感约束，不开细粒度能力。
func RegisterIntentPolicyRoutes(r *gin.RouterGroup, h *handler.IntentPolicyHandler, g *rbacGuards) {
	policies := g.apiKeyGroup(r.Group("/intent-policies"), apiKeyFullAccess())
	{
		// Create policy (version=1) — Admin+
		policies.POST("", g.Admin(), h.CreatePolicy)
		// List all policies of the tenant (all scopes/versions) — Admin+
		policies.GET("", g.Admin(), h.ListPolicies)
		// Read one policy (any historical version) — Admin+
		policies.GET("/:id", g.Admin(), h.GetPolicy)
		// Update = insert version+1 in the same lineage, old version kept — Admin+
		policies.PUT("/:id", g.Admin(), h.UpdatePolicy)
		// Disable / enable in place (no new version) — Admin+
		policies.POST("/:id/disable", g.Admin(), h.DisablePolicy)
		policies.POST("/:id/enable", g.Admin(), h.EnablePolicy)
	}
}

// RegisterIntentVerdictRoutes registers the IntentGate verdict report routes
// (T51，issue #22)：分布汇总 + 单条下钻，只读、Admin+（理由可能含业务
// 上下文，与策略管理同门槛）。
func RegisterIntentVerdictRoutes(r *gin.RouterGroup, h *handler.IntentVerdictHandler, g *rbacGuards) {
	verdicts := g.apiKeyGroup(r.Group("/intent-verdicts"), apiKeyFullAccess())
	{
		verdicts.GET("", g.Admin(), h.ListVerdicts)
		verdicts.GET("/summary", g.Admin(), h.VerdictSummary)
		// T61 语料导出（issue #24）：按 judge 模型分层，Admin+。
		verdicts.GET("/export", g.Admin(), h.ExportCorpus)
	}
}
