package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/agent/intentgate"
	"github.com/Tencent/WeKnora/internal/application/repository"
	weerrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// IntentPolicyHandler 暴露 IntentGate 策略（IntentPolicy，CONTEXT.md
// 术语）的 CRUD REST API（设计文档 §3.2 策略生命周期、§6.1 数据模型）。
//
// 版本化语义：策略谱系由 (tenant_id, scope_type, scope_ref) 标识；
// PUT 更新 = 插入 version+1 新行，旧版本保留且仍可按 ID 查询（对账前提）。
// 停用/启用不产生新版本，原地翻转 enabled。
//
// 所有读写在 handler 层取调用方 tenant_id 作为唯一租户谓词（repository
// 全部查询带 tenant_id），跨租户访问一律 404——不暴露策略存在性。
//
// 任何变更（创建/新版本/启停）后都触发 PolicyStore.InvalidateTenant，
// 使该租户的运行期 scope 解析缓存失效（设计 §8.3）。
type IntentPolicyHandler struct {
	repo interfaces.IntentPolicyRepository
	// store 是运行期 scope 解析缓存（intentgate.PolicyStore）。允许为 nil
	//（部分装配的单测），此时跳过缓存失效。
	store intentgate.PolicyStore
}

// NewIntentPolicyHandler constructs the handler.
func NewIntentPolicyHandler(repo interfaces.IntentPolicyRepository, store intentgate.PolicyStore) *IntentPolicyHandler {
	return &IntentPolicyHandler{repo: repo, store: store}
}

// CreateIntentPolicyRequest 是 POST /intent-policies 的请求体。
// mode 缺省为 observe（新策略一律 Observe 起步，CONTEXT.md）；创建
// enforce 策略必须显式传 "mode":"enforce"。risk_tier 缺省 low。
type CreateIntentPolicyRequest struct {
	ScopeType      string `json:"scope_type" binding:"required"`
	ScopeRef       string `json:"scope_ref"`
	ArgPath        string `json:"arg_path"`
	ConstraintText string `json:"constraint_text" binding:"required"`
	RuleExpr       string `json:"rule_expr"`
	RiskTier       string `json:"risk_tier"`
	Mode           string `json:"mode"`
}

// UpdateIntentPolicyRequest 是 PUT /intent-policies/:id 的请求体：新版本的
// 完整内容（scope 不可改——换 scope 等于换谱系，应新建策略）。缺省值与
// 创建一致（mode→observe、risk_tier→low、arg_path/rule_expr→NULL）；
// 这是安全方向的缺省：漏传 mode 只会把策略降级到 observe，绝不会反向
// 升级到 enforce。
type UpdateIntentPolicyRequest struct {
	ArgPath        string `json:"arg_path"`
	ConstraintText string `json:"constraint_text" binding:"required"`
	RuleExpr       string `json:"rule_expr"`
	RiskTier       string `json:"risk_tier"`
	Mode           string `json:"mode"`
}

func (h *IntentPolicyHandler) tenantID(c *gin.Context) uint64 {
	return c.GetUint64(types.TenantIDContextKey.String())
}

func (h *IntentPolicyHandler) createdBy(c *gin.Context) string {
	userID, _ := c.Get(types.UserIDContextKey.String())
	if s, ok := userID.(string); ok {
		return s
	}
	return ""
}

// invalidate 失效该租户的策略缓存（best-effort，不阻塞响应）。
func (h *IntentPolicyHandler) invalidate(tenantID uint64) {
	if h.store != nil {
		h.store.InvalidateTenant(tenantID)
	}
}

func respondPolicyError(c *gin.Context, err error, action string) {
	if errors.Is(err, repository.ErrIntentPolicyNotFound) {
		c.Error(weerrors.NewNotFoundError("Intent policy not found"))
		return
	}
	logger.Errorf(c.Request.Context(), "[IntentPolicy] %s failed: %v", action, err)
	c.Error(weerrors.NewInternalServerError("Failed to " + action + " intent policy"))
}

// CreatePolicy godoc
// @Summary      创建意图策略
// @Description  创建一条 IntentGate 策略（version=1）。同一谱系（scope_type+scope_ref）已存在时返回 409，修改请用 PUT。mode 缺省 observe。
// @Tags         IntentGate 策略
// @Accept       json
// @Produce      json
// @Param        request  body      CreateIntentPolicyRequest  true  "策略内容"
// @Success      201      {object}  map[string]interface{}  "创建的策略"
// @Failure      400/409  {object}  errors.AppError
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /intent-policies [post]
func (h *IntentPolicyHandler) CreatePolicy(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := h.tenantID(c)

	var req CreateIntentPolicyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(weerrors.NewBadRequestError("Invalid request parameters").WithDetails(err.Error()))
		return
	}

	policy, err := types.NewIntentPolicy(types.IntentPolicyInput{
		TenantID:       tenantID,
		ScopeType:      req.ScopeType,
		ScopeRef:       req.ScopeRef,
		ArgPath:        req.ArgPath,
		ConstraintText: req.ConstraintText,
		RuleExpr:       req.RuleExpr,
		RiskTier:       req.RiskTier,
		Mode:           req.Mode,
		CreatedBy:      h.createdBy(c),
	})
	if err != nil {
		c.Error(weerrors.NewBadRequestError("Invalid intent policy").WithDetails(err.Error()))
		return
	}

	// 谱系唯一性：重复 POST 同 (scope_type, scope_ref) 会绕过版本化语义
	// （设计 §3.2 修改 = version+1），明确拒绝并指向 PUT。
	existing, err := h.repo.ListByTenant(ctx, tenantID)
	if err != nil {
		respondPolicyError(c, err, "list")
		return
	}
	for _, p := range existing {
		if p.ScopeType == req.ScopeType && p.ScopeRef == req.ScopeRef {
			c.Error(weerrors.NewConflictError(
				"Intent policy lineage already exists for this scope; use PUT to create a new version"))
			return
		}
	}

	if err := h.repo.Create(ctx, policy); err != nil {
		respondPolicyError(c, err, "create")
		return
	}
	h.invalidate(tenantID)
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": policy})
}

// ListPolicies godoc
// @Summary      列出意图策略
// @Description  列出当前租户的全部策略（跨 scope、跨 version、含 disabled）。
// @Tags         IntentGate 策略
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "策略列表"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /intent-policies [get]
func (h *IntentPolicyHandler) ListPolicies(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := h.tenantID(c)

	policies, err := h.repo.ListByTenant(ctx, tenantID)
	if err != nil {
		respondPolicyError(c, err, "list")
		return
	}
	if policies == nil {
		policies = []*types.IntentPolicy{}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": policies})
}

// GetPolicy godoc
// @Summary      读取意图策略
// @Description  按 ID 读取一条策略（任一历史版本均可查）。跨租户返回 404。
// @Tags         IntentGate 策略
// @Produce      json
// @Param        id   path      string  true  "策略ID"
// @Success      200  {object}  map[string]interface{}  "策略"
// @Failure      404  {object}  errors.AppError
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /intent-policies/{id} [get]
func (h *IntentPolicyHandler) GetPolicy(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := h.tenantID(c)

	policy, err := h.repo.GetByID(ctx, tenantID, c.Param("id"))
	if err != nil {
		respondPolicyError(c, err, "get")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": policy})
}

// UpdatePolicy godoc
// @Summary      更新意图策略（新版本）
// @Description  在目标策略的谱系上插入 version+1 新行；旧版本保留且仍可查。scope 继承谱系不可改；enabled 继承目标行。
// @Tags         IntentGate 策略
// @Accept       json
// @Produce      json
// @Param        id       path      string                     true  "策略ID（谱系内任一版本）"
// @Param        request  body      UpdateIntentPolicyRequest  true  "新版本内容"
// @Success      200      {object}  map[string]interface{}  "新版本策略"
// @Failure      400/404  {object}  errors.AppError
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /intent-policies/{id} [put]
func (h *IntentPolicyHandler) UpdatePolicy(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := h.tenantID(c)

	var req UpdateIntentPolicyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(weerrors.NewBadRequestError("Invalid request parameters").WithDetails(err.Error()))
		return
	}

	target, err := h.repo.GetByID(ctx, tenantID, c.Param("id"))
	if err != nil {
		respondPolicyError(c, err, "get")
		return
	}

	// 新版本号取谱系（同 scope_type+scope_ref）最大 version+1——PUT 一个
	// 旧版本也必须落在谱系顶端，保证 version 单调递增（设计 §3.2）。
	lineage, err := h.repo.ListByTenant(ctx, tenantID)
	if err != nil {
		respondPolicyError(c, err, "list")
		return
	}
	nextVersion := target.Version + 1
	for _, p := range lineage {
		if p.ScopeType == target.ScopeType && p.ScopeRef == target.ScopeRef && p.Version+1 > nextVersion {
			nextVersion = p.Version + 1
		}
	}

	policy, err := types.NewIntentPolicy(types.IntentPolicyInput{
		TenantID:       tenantID,
		ScopeType:      target.ScopeType,
		ScopeRef:       target.ScopeRef,
		ArgPath:        req.ArgPath,
		ConstraintText: req.ConstraintText,
		RuleExpr:       req.RuleExpr,
		RiskTier:       req.RiskTier,
		Mode:           req.Mode,
		Version:        nextVersion,
		CreatedBy:      h.createdBy(c),
	})
	if err != nil {
		c.Error(weerrors.NewBadRequestError("Invalid intent policy").WithDetails(err.Error()))
		return
	}
	// 继承目标行的启停状态：更新内容不应悄悄复活一条已停用的策略。
	// 注意不能仅靠 policy.Enabled=false 走 Create——该列带 gorm
	// default:true 标签，零值会被 INSERT 省略回落到 DB 默认值；
	// 必须在插入后显式翻转。
	if err := h.repo.Create(ctx, policy); err != nil {
		respondPolicyError(c, err, "update")
		return
	}
	if !target.Enabled {
		if err := h.repo.SetEnabled(ctx, tenantID, policy.ID, false); err != nil {
			respondPolicyError(c, err, "set enabled on")
			return
		}
		policy.Enabled = false
	}
	h.invalidate(tenantID)
	c.JSON(http.StatusOK, gin.H{"success": true, "data": policy})
}

// setEnabled 是 disable/enable 的共用实现。
func (h *IntentPolicyHandler) setEnabled(c *gin.Context, enabled bool) {
	ctx := c.Request.Context()
	tenantID := h.tenantID(c)
	id := c.Param("id")

	if err := h.repo.SetEnabled(ctx, tenantID, id, enabled); err != nil {
		respondPolicyError(c, err, "set enabled on")
		return
	}
	h.invalidate(tenantID)

	policy, err := h.repo.GetByID(ctx, tenantID, id)
	if err != nil {
		respondPolicyError(c, err, "get")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": policy})
}

// DisablePolicy godoc
// @Summary      停用意图策略
// @Description  停用一条策略（enabled=false，原地翻转，不产生新版本）。停用后不再参与 scope 解析。
// @Tags         IntentGate 策略
// @Produce      json
// @Param        id   path      string  true  "策略ID"
// @Success      200  {object}  map[string]interface{}  "停用后的策略"
// @Failure      404  {object}  errors.AppError
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /intent-policies/{id}/disable [post]
func (h *IntentPolicyHandler) DisablePolicy(c *gin.Context) { h.setEnabled(c, false) }

// EnablePolicy godoc
// @Summary      启用意图策略
// @Description  重新启用一条已停用的策略。
// @Tags         IntentGate 策略
// @Produce      json
// @Param        id   path      string  true  "策略ID"
// @Success      200  {object}  map[string]interface{}  "启用后的策略"
// @Failure      404  {object}  errors.AppError
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /intent-policies/{id}/enable [post]
func (h *IntentPolicyHandler) EnablePolicy(c *gin.Context) { h.setEnabled(c, true) }
