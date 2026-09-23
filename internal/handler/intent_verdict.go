// IntentGate 判定报表接口（T51，issue #22）：每策略的 verdict 分布与
// 单条下钻。只读、Admin+（与策略管理同门槛：verdict 理由可能含业务
// 上下文，不对普通成员开放）。
package handler

import (
	"net/http"
	"strconv"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

type IntentVerdictHandler struct {
	repo interfaces.IntentVerdictRepository
}

func NewIntentVerdictHandler(repo interfaces.IntentVerdictRepository) *IntentVerdictHandler {
	return &IntentVerdictHandler{repo: repo}
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
