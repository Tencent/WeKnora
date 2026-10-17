package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/im"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
)

// sandboxConfigLookup is the existence check an agent's sandbox selection needs.
// Narrower than the full config service so this handler cannot grow a dependency
// on config mutation.
type sandboxConfigLookup interface {
	Get(ctx context.Context, tenantID uint64, id string) (*types.TenantSandboxConfigEntity, error)
}

// CustomAgentHandler defines the HTTP handler for custom agent operations
type CustomAgentHandler struct {
	service      interfaces.CustomAgentService
	imService    *im.Service
	disabledRepo interfaces.TenantDisabledSharedAgentRepository
	// userService 仅用于 list 接口批量回填 creator_name，作用见
	// KnowledgeBaseHandler.userService。
	userService interfaces.UserService
	// sandboxConfigs validates an agent's sandbox backend selection. Optional —
	// nil in partially-wired unit tests, where the selection is left unchecked.
	sandboxConfigs sandboxConfigLookup
	usableSkills   usableSkillLister
	desktop        bool
}

// NewCustomAgentHandler creates a new custom agent handler instance
func NewCustomAgentHandler(
	service interfaces.CustomAgentService,
	imService *im.Service,
	disabledRepo interfaces.TenantDisabledSharedAgentRepository,
	userService interfaces.UserService,
	sandboxConfigs *service.TenantSandboxConfigService,
	host service.HostSandboxManager,
	usableSkills *service.TenantSkillService,
) *CustomAgentHandler {
	return &CustomAgentHandler{
		service:        service,
		imService:      imService,
		disabledRepo:   disabledRepo,
		userService:    userService,
		sandboxConfigs: sandboxConfigs,
		usableSkills:   usableSkills,
		desktop:        host.Desktop,
	}
}

// CreateAgentRequest defines the request body for creating an agent
type CreateAgentRequest struct {
	// Agent name
	Name string `json:"name" binding:"required" extensions:"x-agent"`
	// What the agent is for
	Description string                  `json:"description" extensions:"x-agent"`
	Avatar      string                  `json:"avatar"`
	Config      types.CustomAgentConfig `json:"config"`
}

// PatchAgentRequest changes only the fields it carries; everything else in
// the agent, its config included, is kept. The editor saves with PUT, which
// replaces the whole config; this is for callers that change one setting.
type PatchAgentRequest struct {
	// Agent name
	Name *string `json:"name" extensions:"x-agent"`
	// What the agent is for
	Description *string `json:"description" extensions:"x-agent"`
	// Running mode: quick-answer is RAG; smart-reasoning calls tools, MCP and skills
	AgentMode *string `json:"agent_mode" enums:"quick-answer,smart-reasoning" extensions:"x-agent"`
	// The whole system prompt
	SystemPrompt *string `json:"system_prompt" extensions:"x-agent,x-agent-max-bytes=32000"`
	// Chat model the agent answers with
	ModelID *string `json:"model_id" extensions:"x-agent"`
	// Knowledge bases searched: all, selected (knowledge_bases) or none
	KBSelectionMode *string `json:"kb_selection_mode" enums:"all,selected,none" extensions:"x-agent"`
	// Selected knowledge bases, replacing the current ones; without
	// kb_selection_mode it also switches the mode to selected
	KnowledgeBases *[]string `json:"knowledge_bases" extensions:"x-agent=knowledge_base_ids,x-agent-maximum=50"`
	// MCP services used: all, selected (mcp_services) or none
	MCPSelectionMode *string `json:"mcp_selection_mode" enums:"all,selected,none" extensions:"x-agent"`
	// Selected MCP services, replacing the current ones; without
	// mcp_selection_mode it also switches the mode to selected
	MCPServices *[]string `json:"mcp_services" extensions:"x-agent=mcp_service_ids,x-agent-maximum=50"`
	// Skills used: all, selected (selected_skills) or none
	SkillsSelectionMode *string `json:"skills_selection_mode" enums:"all,selected,none" extensions:"x-agent"`
	// Selected skill names, replacing the current ones; without
	// skills_selection_mode it also switches the mode to selected
	SelectedSkills *[]string `json:"selected_skills" extensions:"x-agent,x-agent-maximum=50"`
	// Whether the agent may search the web
	WebSearchEnabled *bool `json:"web_search_enabled" extensions:"x-agent"`
	// Whether earlier turns are kept in the conversation context
	MultiTurnEnabled *bool `json:"multi_turn_enabled" extensions:"x-agent"`
}

// apply overlays the request onto an agent's current state, as an update
// request that keeps the avatar.
func (p PatchAgentRequest) apply(agent *types.CustomAgent) UpdateAgentRequest {
	req := UpdateAgentRequest{Name: agent.Name, Description: agent.Description, Config: agent.Config}
	if p.Name != nil {
		req.Name = *p.Name
	}
	if p.Description != nil {
		req.Description = *p.Description
	}
	cfg := &req.Config
	if p.AgentMode != nil {
		cfg.AgentMode = strings.TrimSpace(*p.AgentMode)
	}
	if p.SystemPrompt != nil {
		cfg.SystemPrompt = *p.SystemPrompt
	}
	if p.ModelID != nil {
		cfg.ModelID = *p.ModelID
	}
	if p.KnowledgeBases != nil {
		cfg.KnowledgeBases = append([]string{}, *p.KnowledgeBases...)
		cfg.KBSelectionMode = "selected"
	}
	if p.KBSelectionMode != nil {
		cfg.KBSelectionMode = *p.KBSelectionMode
	}
	if p.MCPServices != nil {
		cfg.MCPServices = append([]string{}, *p.MCPServices...)
		cfg.MCPSelectionMode = "selected"
	}
	if p.MCPSelectionMode != nil {
		cfg.MCPSelectionMode = *p.MCPSelectionMode
	}
	if p.SelectedSkills != nil {
		cfg.SelectedSkills = append([]string{}, *p.SelectedSkills...)
		cfg.SkillsSelectionMode = "selected"
	}
	if p.SkillsSelectionMode != nil {
		cfg.SkillsSelectionMode = *p.SkillsSelectionMode
	}
	if p.SelectedSkills != nil || p.SkillsSelectionMode != nil {
		if cfg.SkillsSelectionMode == "all" || cfg.SkillsSelectionMode == "none" {
			cfg.SelectedSkills = []string{}
		}
	}
	if p.WebSearchEnabled != nil {
		cfg.WebSearchEnabled = *p.WebSearchEnabled
	}
	if p.MultiTurnEnabled != nil {
		cfg.MultiTurnEnabled = *p.MultiTurnEnabled
	}
	return req
}

func validAgentMode(mode *string) bool {
	if mode == nil {
		return true
	}
	switch strings.TrimSpace(*mode) {
	case types.AgentModeQuickAnswer, types.AgentModeSmartReasoning:
		return true
	}
	return false
}

func validSelectionMode(mode *string) bool {
	if mode == nil {
		return true
	}
	switch *mode {
	case "all", "selected", "none":
		return true
	}
	return false
}

// UpdateAgentRequest defines the request body for updating an agent
type UpdateAgentRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// Avatar travels as a pointer so an omitted field can be told apart from
	// an explicit clear: nil keeps the stored avatar, a pointer to "" wipes
	// it. As a plain string the two cases were indistinguishable, so a caller
	// that PUT only a config silently zeroed the avatar and still got a 200.
	Avatar *string                 `json:"avatar"`
	Config types.CustomAgentConfig `json:"config"`
}

// CreateAgent godoc
// @Summary      创建智能体
// @Description  创建新的自定义智能体
// @Tags         智能体
// @Accept       json
// @Produce      json
// @Param        request  body      CreateAgentRequest  true  "智能体信息"
// @Success      201      {object}  handler.AgentWriteResponse  "创建的智能体"
// @Failure      400      {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @x-agent      {"id":"agent.create","risk":"write","label_param":"name"}
// @x-agent-hint "Creates a smart-reasoning agent with default settings, owned by the user. It can call MCP services and skills. Configure it afterwards with agent.update."
// @Router       /agents [post]
func (h *CustomAgentHandler) CreateAgent(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start creating custom agent")

	// Parse request body
	var req CreateAgentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		c.Error(errors.NewBadRequestError("Invalid request parameters").WithDetails(err.Error()))
		return
	}
	if err := authorizeAgentKnowledgeScope(ctx, req.Config); err != nil {
		c.Error(err)
		return
	}
	if err := h.validateAgentSandboxConfig(ctx, req.Config); err != nil {
		c.Error(err)
		return
	}
	if err := normalizeAgentReasoningEffort(&req.Config); err != nil {
		_ = c.Error(err)
		return
	}
	toolWarnings, err := checkAgentAllowedTools(nil, &req.Config)
	if err != nil {
		_ = c.Error(err)
		return
	}

	// Build agent object
	agent := &types.CustomAgent{
		Name:        req.Name,
		Description: req.Description,
		Avatar:      req.Avatar,
		Config:      req.Config,
	}
	agent.EnsureDefaults()
	// The DB column (varchar(64)) has no application-level guard, so an
	// oversized avatar used to reach postgres and come back as a raw driver
	// 500. Reject it here with a 400 that names the limit.
	if err := agent.ValidateAvatar(); err != nil {
		_ = c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	if err := agent.Config.QuestionSuggestions.Validate(); err != nil {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	logger.Infof(ctx, "Creating custom agent, name: %s, agent_mode: %s",
		secutils.SanitizeForLog(req.Name), req.Config.AgentMode)

	// Create agent using the service
	createdAgent, err := h.service.CreateAgent(ctx, agent)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		if err == service.ErrAgentNameRequired {
			c.Error(errors.NewBadRequestError(err.Error()))
			return
		}
		// Reached only after the typed sentinels and *errors.AppError
		// above, so whatever lands here is a raw repository/driver error.
		// Its text (SQLSTATE, column types) must not reach the client;
		// the full detail is already logged above.
		_ = c.Error(errors.NewInternalServerError("Failed to create agent"))
		return
	}

	logger.Infof(ctx, "Custom agent created successfully, ID: %s, name: %s",
		secutils.SanitizeForLog(createdAgent.ID), secutils.SanitizeForLog(createdAgent.Name))
	resp := gin.H{
		"success": true,
		"data":    createdAgent,
	}
	if toolWarnings != nil {
		resp["allowed_tools_warnings"] = toolWarnings
	}
	c.JSON(http.StatusCreated, resp)
}

// GetAgent godoc
// @Summary      获取智能体详情
// @Description  根据ID获取智能体详情
// @Tags         智能体
// @Accept       json
// @Produce      json
// @Param        id   path      string  true  "智能体ID"  extensions(x-agent=agent_id)
// @Success      200  {object}  handler.DataResponse{data=types.CustomAgent}  "智能体详情"
// @Failure      400  {object}  errors.AppError         "请求参数错误"
// @Failure      404  {object}  errors.AppError         "智能体不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @x-agent      {"id":"agent.get","risk":"read"}
// @x-agent-hint "Returns an agent's settings, including its full system prompt, tools and MCP and skill scope."
// @Router       /agents/{id} [get]
func (h *CustomAgentHandler) GetAgent(c *gin.Context) {
	ctx := c.Request.Context()

	// Get agent ID from URL parameter
	id := secutils.SanitizeForLog(c.Param("id"))
	if id == "" {
		logger.Error(ctx, "Agent ID is empty")
		c.Error(errors.NewBadRequestError("Agent ID cannot be empty"))
		return
	}

	agent, err := h.service.GetAgentByID(ctx, id)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"agent_id": id,
		})
		if err == service.ErrAgentNotFound {
			c.Error(errors.NewNotFoundError("Agent not found"))
			return
		}
		if appErr, ok := err.(*errors.AppError); ok {
			c.Error(appErr)
			return
		}
		// Reached only after the typed sentinels and *errors.AppError above, so
		// whatever lands here is a raw repository/driver error. Its text
		// (SQLSTATE, column names) must not reach the client; the full detail
		// is already logged above.
		_ = c.Error(errors.NewInternalServerError("Failed to load agent"))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    agent,
	})
}

// ListAgents godoc
// @Summary      获取智能体列表
// @Description  获取当前空间的所有智能体（包括内置智能体）
// @Tags         智能体
// @Accept       json
// @Produce      json
// @Param        creator  query  string  false  "创建者过滤，仅 mine 与 others 生效"  Enums(mine, others)  extensions(x-agent)
// @Success      200  {object}  handler.ListAgentsResponse  "智能体列表"
// @Failure      500  {object}  errors.AppError         "服务器错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @x-agent      {"id":"agent.list","risk":"read","max_items":200}
// @x-agent-hint "Lists the workspace's agents, built-in ones included. Built-in agents are shared by everyone; only an admin can change them."
// @Router       /agents [get]
func (h *CustomAgentHandler) ListAgents(c *gin.Context) {
	ctx := c.Request.Context()

	// Get all agents for this tenant
	agents, err := h.service.ListAgents(ctx)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		// Reached only after the typed sentinels and *errors.AppError above, so
		// whatever lands here is a raw repository/driver error. Its text
		// (SQLSTATE, column names) must not reach the client; the full detail
		// is already logged above.
		_ = c.Error(errors.NewInternalServerError("Failed to list agents"))
		return
	}

	// Optional creator filter — see the matching block in
	// KnowledgeBaseHandler.ListKnowledgeBases for rationale. Built-in
	// agents (IsBuiltin=true, CreatedBy="") are tenant-level fixtures
	// rather than user creations; we always keep them regardless of the
	// filter so the conversation dropdown never silently loses
	// quick-answer / smart-reasoning when a user picks "Created by me".
	creatorFilter := strings.ToLower(strings.TrimSpace(c.Query("creator")))
	if creatorFilter == "mine" || creatorFilter == "others" {
		callerUserID, _ := c.Get(types.UserIDContextKey.String())
		callerUserIDStr, _ := callerUserID.(string)
		filtered := make([]*types.CustomAgent, 0, len(agents))
		for _, ag := range agents {
			if ag.IsBuiltin {
				filtered = append(filtered, ag)
				continue
			}
			if ag.CreatedBy == "" {
				continue
			}
			if creatorFilter == "mine" && ag.CreatedBy == callerUserIDStr {
				filtered = append(filtered, ag)
			} else if creatorFilter == "others" && ag.CreatedBy != callerUserIDStr {
				filtered = append(filtered, ag)
			}
		}
		agents = filtered
	}

	// Per-tenant "disabled by me" for own agents (only affects this tenant's conversation dropdown)
	tenantIDVal, exists := c.Get(types.TenantIDContextKey.String())
	if !exists {
		logger.Error(ctx, "Workspace ID not found in context")
		c.Error(errors.NewUnauthorizedError("Missing workspace context"))
		return
	}
	tenantID, ok := tenantIDVal.(uint64)
	if !ok {
		logger.Errorf(ctx, "Tenant ID has unexpected type %T in context", tenantIDVal)
		c.Error(errors.NewInternalServerError("Invalid workspace context type"))
		return
	}
	disabledOwnIDs, err := h.disabledRepo.ListDisabledOwnAgentIDs(ctx, tenantID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"tenant_id": tenantID,
		})
		c.Error(errors.NewInternalServerError("Failed to list disabled agent IDs: " + err.Error()))
		return
	}

	// 批量回填 creator_name，作用同 KB 列表：让前端能区分「我创建」与「同空间其他成员」。
	// 内建 agent（IsBuiltin=true, CreatedBy=""）不会有 creator_name，前端按 builtin
	// 分支单独渲染。
	enrichAgentCreatorNames(ctx, h.userService, agents)

	c.JSON(http.StatusOK, gin.H{
		"success":                true,
		"data":                   agents,
		"disabled_own_agent_ids": disabledOwnIDs,
	})
}

// enrichAgentCreatorNames 批量把 agent.CreatedBy 解析成展示名。失败吞掉，
// 不影响列表本身可用。与 enrichKBCreatorNames 行为对齐。
func enrichAgentCreatorNames(ctx context.Context, userSvc interfaces.UserService, agents []*types.CustomAgent) {
	if userSvc == nil || len(agents) == 0 {
		return
	}
	idSet := make(map[string]struct{}, len(agents))
	for _, ag := range agents {
		if ag.IsBuiltin || ag.CreatedBy == "" {
			continue
		}
		idSet[ag.CreatedBy] = struct{}{}
	}
	if len(idSet) == 0 {
		return
	}
	ids := make([]string, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
	}
	users, err := userSvc.GetUsersByIDs(ctx, ids)
	if err != nil {
		logger.Warnf(ctx, "Failed to resolve agent creator names: %v", err)
		return
	}
	for _, ag := range agents {
		if ag.IsBuiltin || ag.CreatedBy == "" {
			continue
		}
		u, ok := users[ag.CreatedBy]
		if !ok || u == nil {
			continue
		}
		ag.CreatorName = pickUserDisplayName(u)
	}
}

// UpdateAgent godoc
// @Summary      更新智能体
// @Description  更新智能体的名称、描述和配置
// @Tags         智能体
// @Accept       json
// @Produce      json
// @Param        id       path      string              true  "智能体ID"
// @Param        request  body      UpdateAgentRequest  true  "更新请求"
// @Success      200      {object}  handler.AgentWriteResponse  "更新后的智能体"
// @Failure      400      {object}  errors.AppError         "请求参数错误"
// @Failure      403      {object}  errors.AppError         "无法修改内置智能体"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /agents/{id} [put]
func (h *CustomAgentHandler) UpdateAgent(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start updating custom agent")

	// Get agent ID from URL parameter
	id := secutils.SanitizeForLog(c.Param("id"))
	if id == "" {
		logger.Error(ctx, "Agent ID is empty")
		c.Error(errors.NewBadRequestError("Agent ID cannot be empty"))
		return
	}

	// Parse request body
	var req UpdateAgentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		c.Error(errors.NewBadRequestError("Invalid request parameters").WithDetails(err.Error()))
		return
	}
	h.saveAgentUpdate(c, id, req)
}

// PatchAgent godoc
// @Summary      部分更新智能体
// @Description  只修改请求中出现的字段，其余配置保持不变。
// @Description  支持名称、描述、运行模式、系统提示词、对话模型、知识库、MCP、技能范围、联网搜索、多轮对话。
// @Description  agent_mode 取 quick-answer 或 smart-reasoning。MCP 与技能只在 smart-reasoning 下运行，切到该模式会打开多轮对话。
// @Description  传 knowledge_bases、mcp_services 或 selected_skills 而不传对应模式时，切换为 selected。
// @Description  技能模式 all/none 清空技能列表；启用技能须先在编辑器选择沙箱，selected 名称须可用，否则返回 400。
// @Tags         智能体
// @Accept       json
// @Produce      json
// @Param        id       path      string             true  "智能体ID"  extensions(x-agent=agent_id)
// @Param        request  body      PatchAgentRequest  true  "要修改的字段"
// @Success      200      {object}  handler.AgentWriteResponse  "更新后的智能体"
// @Failure      400      {object}  errors.AppError         "请求参数错误"
// @Failure      403      {object}  errors.AppError         "无权修改该智能体"
// @Failure      404      {object}  errors.AppError         "智能体不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @x-agent      {"id":"agent.update","risk":"write","target":"agent.get","label":"name","require_complete_target":true}
// @x-agent-hint "Changes the listed settings of an agent; a field left out is kept. agent_mode is quick-answer or smart-reasoning; MCP services and skills only run in smart-reasoning, and switching to it turns multi-turn on. system_prompt replaces the whole prompt, so start from the full prompt agent.get returns. Take model_id from model.list (a KnowledgeQA model), knowledge base ids from knowledge_base.list and MCP service ids from mcp_service.list. Take skill names exactly from skill.list with the agent's sandbox_config_id. knowledge_base_ids, mcp_service_ids or selected_skills alone also switch that scope to selected."
// @Router       /agents/{id} [patch]
func (h *CustomAgentHandler) PatchAgent(c *gin.Context) {
	ctx := c.Request.Context()

	id := secutils.SanitizeForLog(c.Param("id"))
	if id == "" {
		_ = c.Error(errors.NewBadRequestError("Agent ID cannot be empty"))
		return
	}
	var patch PatchAgentRequest
	if err := c.ShouldBindJSON(&patch); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		_ = c.Error(errors.NewBadRequestError("Invalid request parameters").WithDetails(err.Error()))
		return
	}
	if !validAgentMode(patch.AgentMode) {
		_ = c.Error(errors.NewBadRequestError("agent_mode must be quick-answer or smart-reasoning"))
		return
	}
	if !validSelectionMode(patch.KBSelectionMode) || !validSelectionMode(patch.MCPSelectionMode) ||
		!validSelectionMode(patch.SkillsSelectionMode) {
		_ = c.Error(errors.NewBadRequestError("selection mode must be all, selected or none"))
		return
	}
	existing, err := h.service.GetAgentByID(ctx, id)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"agent_id": id})
		if err == service.ErrAgentNotFound {
			_ = c.Error(errors.NewNotFoundError("Agent not found"))
			return
		}
		_ = c.Error(errors.NewInternalServerError("Failed to update agent"))
		return
	}
	req := patch.apply(existing)
	if patch.SelectedSkills != nil && len(*patch.SelectedSkills) > 50 {
		_ = c.Error(errors.NewBadRequestError("selected_skills must contain at most 50 names"))
		return
	}
	skillTarget := req.Config.SandboxConfigID
	if h.desktop {
		// Lite agents store no sandbox config; their skills run on the host.
		skillTarget = sandbox.HostSkillTargetID
	}
	// Skills already saved are not re-checked: a skill uninstalled since must
	// not block an unrelated change such as a rename.
	if patch.SkillsSelectionMode != nil || patch.SelectedSkills != nil {
		if (req.Config.SkillsSelectionMode == "all" || req.Config.SkillsSelectionMode == "selected") &&
			skillTarget == "" {
			_ = c.Error(errors.NewBadRequestError("Choose a sandbox in the agent editor before selecting skills"))
			return
		}
		if err := h.validateAgentSkillNames(ctx, req.Config, skillTarget); err != nil {
			_ = c.Error(err)
			return
		}
	}
	h.saveAgentUpdate(c, id, req)
}

func (h *CustomAgentHandler) validateAgentSkillNames(
	ctx context.Context, cfg types.CustomAgentConfig, skillTarget string,
) error {
	if cfg.SkillsSelectionMode != "selected" || len(cfg.SelectedSkills) == 0 {
		return nil
	}
	if h.usableSkills == nil {
		return errors.NewInternalServerError("Failed to verify selected skills")
	}
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok {
		return errors.NewUnauthorizedError("Missing workspace context")
	}
	usable := map[string]bool{}
	for _, skill := range h.usableSkills.ListUsableSkills(ctx, tenantID, skillTarget) {
		if skill != nil {
			usable[skill.Name] = true
		}
	}
	var unknown []string
	for _, name := range cfg.SelectedSkills {
		if !usable[name] {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		return errors.NewBadRequestError("Unknown or unavailable skills: " + strings.Join(unknown, ", "))
	}
	return nil
}

// saveAgentUpdate validates and stores a full update of agent id, for both
// PUT and PATCH.
func (h *CustomAgentHandler) saveAgentUpdate(c *gin.Context, id string, req UpdateAgentRequest) {
	ctx := c.Request.Context()
	if err := authorizeAgentKnowledgeScope(ctx, req.Config); err != nil {
		c.Error(err)
		return
	}
	if err := h.validateAgentSandboxConfig(ctx, req.Config); err != nil {
		c.Error(err)
		return
	}
	if err := normalizeAgentReasoningEffort(&req.Config); err != nil {
		_ = c.Error(err)
		return
	}
	// Names already stored are kept, so the stored list is needed first.
	existing, err := h.service.GetAgentByID(ctx, id)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"agent_id": id})
		if err == service.ErrAgentNotFound {
			_ = c.Error(errors.NewNotFoundError("Agent not found"))
			return
		}
		_ = c.Error(errors.NewInternalServerError("Failed to update agent"))
		return
	}
	toolWarnings, err := checkAgentAllowedTools(existing.Config.AllowedTools, &req.Config)
	if err != nil {
		_ = c.Error(err)
		return
	}

	// Only a sent avatar is validated: nil means the caller never touched the
	// field, so there is no value to bound. Checking the length here is what
	// turns an oversized avatar into a 400 that names the limit, instead of
	// the 500 that used to carry the database's own varchar(64) complaint.
	if req.Avatar != nil {
		if err := (&types.CustomAgent{Avatar: *req.Avatar}).ValidateAvatar(); err != nil {
			logger.Error(ctx, "Invalid avatar", err)
			_ = c.Error(errors.NewBadRequestError(err.Error()))
			return
		}
	}

	// Build agent object. Avatar is deliberately absent here — it reaches the
	// service as a separate pointer, so that "not sent" survives the trip.
	agent := &types.CustomAgent{
		ID:          id,
		Name:        req.Name,
		Description: req.Description,
		Config:      req.Config,
	}
	agent.EnsureDefaults()
	if err := agent.Config.QuestionSuggestions.Validate(); err != nil {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	logger.Infof(ctx, "Updating custom agent, ID: %s, name: %s",
		secutils.SanitizeForLog(id), secutils.SanitizeForLog(req.Name))

	// Update the agent
	updatedAgent, err := h.service.UpdateAgent(ctx, agent, req.Avatar)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"agent_id": id,
		})
		switch err {
		case service.ErrAgentNotFound:
			c.Error(errors.NewNotFoundError("Agent not found"))
		case service.ErrCannotModifyBuiltin:
			c.Error(errors.NewForbiddenError("Cannot modify built-in agent"))
		case service.ErrAgentNameRequired:
			c.Error(errors.NewBadRequestError(err.Error()))
		case service.ErrAgentKBScopeNotShareable:
			_ = c.Error(errors.NewForbiddenError(err.Error()))
		default:
			// Reached only after the typed sentinels and *errors.AppError above, so
			// whatever lands here is a raw repository/driver error. Its text
			// (SQLSTATE, column names) must not reach the client; the full detail
			// is already logged above.
			_ = c.Error(errors.NewInternalServerError("Failed to update agent"))
		}
		return
	}

	logger.Infof(ctx, "Custom agent updated successfully, ID: %s", secutils.SanitizeForLog(id))
	resp := gin.H{
		"success": true,
		"data":    updatedAgent,
	}
	if toolWarnings != nil {
		resp["allowed_tools_warnings"] = toolWarnings
	}
	c.JSON(http.StatusOK, resp)
}

// DeleteAgent godoc
// @Summary      删除智能体
// @Description  删除指定的智能体
// @Tags         智能体
// @Accept       json
// @Produce      json
// @Param        id   path      string  true  "智能体ID"  extensions(x-agent=agent_id)
// @Success      200  {object}  handler.MessageResponse  "删除成功"
// @Failure      400  {object}  errors.AppError         "请求参数错误"
// @Failure      403  {object}  errors.AppError         "无法删除内置智能体"
// @Failure      404  {object}  errors.AppError         "智能体不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @x-agent      {"id":"agent.delete","risk":"destructive","target":"agent.get","label":"name"}
// @x-agent-hint "Deletes an agent and its IM channels. Cannot be undone; built-in agents cannot be deleted."
// @Router       /agents/{id} [delete]
func (h *CustomAgentHandler) DeleteAgent(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start deleting custom agent")

	// Get agent ID from URL parameter
	id := secutils.SanitizeForLog(c.Param("id"))
	if id == "" {
		logger.Error(ctx, "Agent ID is empty")
		c.Error(errors.NewBadRequestError("Agent ID cannot be empty"))
		return
	}

	logger.Infof(ctx, "Deleting custom agent, ID: %s", secutils.SanitizeForLog(id))

	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok {
		c.Error(errors.NewUnauthorizedError("Unauthorized"))
		return
	}

	if err := h.imService.DeleteChannelsByAgent(id, tenantID); err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"agent_id": id,
		})
		c.Error(errors.NewInternalServerError("Failed to delete agent IM channels"))
		return
	}

	// Delete the agent
	err := h.service.DeleteAgent(ctx, id)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"agent_id": id,
		})
		switch err {
		case service.ErrAgentNotFound:
			c.Error(errors.NewNotFoundError("Agent not found"))
		case service.ErrCannotDeleteBuiltin:
			c.Error(errors.NewForbiddenError("Cannot delete built-in agent"))
		default:
			// Reached only after the typed sentinels and *errors.AppError above, so
			// whatever lands here is a raw repository/driver error. Its text
			// (SQLSTATE, column names) must not reach the client; the full detail
			// is already logged above.
			_ = c.Error(errors.NewInternalServerError("Failed to delete agent"))
		}
		return
	}

	logger.Infof(ctx, "Custom agent deleted successfully, ID: %s", secutils.SanitizeForLog(id))
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Agent deleted successfully",
	})
}

// CopyAgent godoc
// @Summary      复制智能体
// @Description  复制指定的智能体
// @Tags         智能体
// @Accept       json
// @Produce      json
// @Param        id   path      string  true  "智能体ID"  extensions(x-agent=agent_id)
// @Success      201  {object}  handler.DataResponse{data=types.CustomAgent}  "复制成功"
// @Failure      400  {object}  errors.AppError         "请求参数错误"
// @Failure      404  {object}  errors.AppError         "智能体不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @x-agent      {"id":"agent.copy","risk":"write","target":"agent.get","label":"name"}
// @x-agent-hint "Copies an agent, settings included; the copy belongs to the user."
// @Router       /agents/{id}/copy [post]
func (h *CustomAgentHandler) CopyAgent(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start copying custom agent")

	// Get agent ID from URL parameter
	id := secutils.SanitizeForLog(c.Param("id"))
	if id == "" {
		logger.Error(ctx, "Agent ID is empty")
		c.Error(errors.NewBadRequestError("Agent ID cannot be empty"))
		return
	}

	logger.Infof(ctx, "Copying custom agent, ID: %s", secutils.SanitizeForLog(id))
	sourceAgent, err := h.service.GetAgentByID(ctx, id)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"agent_id": id,
		})
		switch err {
		case service.ErrAgentNotFound:
			c.Error(errors.NewNotFoundError("Agent not found"))
		default:
			// Reached only after the typed sentinels and *errors.AppError above, so
			// whatever lands here is a raw repository/driver error. Its text
			// (SQLSTATE, column names) must not reach the client; the full detail
			// is already logged above.
			_ = c.Error(errors.NewInternalServerError("Failed to copy agent"))
		}
		return
	}
	if err := authorizeAgentKnowledgeScope(ctx, sourceAgent.Config); err != nil {
		c.Error(err)
		return
	}

	// Copy the agent
	copiedAgent, err := h.service.CopyAgent(ctx, id)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"agent_id": id,
		})
		switch err {
		case service.ErrAgentNotFound:
			c.Error(errors.NewNotFoundError("Agent not found"))
		default:
			// Reached only after the typed sentinels and *errors.AppError above, so
			// whatever lands here is a raw repository/driver error. Its text
			// (SQLSTATE, column names) must not reach the client; the full detail
			// is already logged above.
			_ = c.Error(errors.NewInternalServerError("Failed to copy agent"))
		}
		return
	}

	logger.Infof(ctx, "Custom agent copied successfully, source ID: %s, new ID: %s",
		secutils.SanitizeForLog(id), secutils.SanitizeForLog(copiedAgent.ID))
	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data":    copiedAgent,
	})
}

// GetPlaceholders godoc
// @Summary      获取占位符定义
// @Description  获取所有可用的提示词占位符定义，按字段类型分组
// @Tags         智能体
// @Accept       json
// @Produce      json
// @Success      200  {object}  handler.DataResponse{data=handler.PlaceholderGroups}  "占位符定义"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /agents/placeholders [get]
func (h *CustomAgentHandler) GetPlaceholders(c *gin.Context) {
	// Return all placeholder definitions grouped by field type
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"all":                   types.AllPlaceholders(),
			"system_prompt":         types.PlaceholdersByField(types.PromptFieldSystemPrompt),
			"agent_system_prompt":   types.PlaceholdersByField(types.PromptFieldAgentSystemPrompt),
			"context_template":      types.PlaceholdersByField(types.PromptFieldContextTemplate),
			"rewrite_system_prompt": types.PlaceholdersByField(types.PromptFieldRewriteSystemPrompt),
			"rewrite_prompt":        types.PlaceholdersByField(types.PromptFieldRewritePrompt),
			"fallback_prompt":       types.PlaceholdersByField(types.PromptFieldFallbackPrompt),
		},
	})
}

// GetAgentTypePresets godoc
// @Summary      获取智能体类型预设列表
// @Description  返回所有 smart-reasoning 下可用的智能体类型预设（RAG/Wiki/Hybrid/Custom），用于编辑器自动填充系统提示词、工具和 KB 兼容性
// @Tags         智能体
// @Accept       json
// @Produce      json
// @Success      200  {object}  handler.DataResponse{data=[]types.AgentTypePresetEntry}  "预设列表"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /agents/type-presets [get]
func (h *CustomAgentHandler) GetAgentTypePresets(c *gin.Context) {
	ctx := c.Request.Context()
	presets := types.ListAgentTypePresetsWithContext(ctx)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    presets,
	})
}

// GetAgentTools godoc
// @Summary      获取智能体可选工具
// @Description  返回编辑器可勾选的内置工具（含分组和知识库能力要求）、工具列表未设置时生效的默认工具，以及由开关控制、写入工具列表也不生效的工具
// @Tags         智能体
// @Accept       json
// @Produce      json
// @Success      200  {object}  handler.DataResponse{data=tools.AgentToolCatalog}  "工具目录"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /agents/tools [get]
func (h *CustomAgentHandler) GetAgentTools(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    tools.BuildAgentToolCatalog(),
	})
}

// GetSuggestedQuestions godoc
// @Summary      获取推荐问题
// @Description  基于智能体关联的知识库，返回推荐问题供用户快捷提问
// @Tags         智能体
// @Accept       json
// @Produce      json
// @Param        id                  path      string  true   "智能体ID"
// @Param        knowledge_base_ids  query     string  false  "知识库ID列表（逗号分隔），覆盖智能体默认配置"
// @Param        knowledge_ids       query     string  false  "知识ID列表（逗号分隔），限定到具体文档"
// @Param        tag_scopes          query     string  false  "带知识库归属的标签范围（JSON）"
// @Param        limit               query     int     false  "返回数量上限（未传时使用智能体配置的开场问题数量，最大30）"
// @Success      200                 {object}  handler.DataResponse{data=handler.SuggestedQuestionsData}  "推荐问题列表"
// @Failure      400                 {object}  errors.AppError         "请求参数错误"
// @Failure      404                 {object}  errors.AppError         "智能体不存在"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /agents/{id}/suggested-questions [get]
func (h *CustomAgentHandler) GetSuggestedQuestions(c *gin.Context) {
	ctx := c.Request.Context()

	// Get agent ID from URL parameter
	id := secutils.SanitizeForLog(c.Param("id"))
	if id == "" {
		logger.Error(ctx, "Agent ID is empty")
		c.Error(errors.NewBadRequestError("Agent ID cannot be empty"))
		return
	}

	// Parse optional query parameters
	var kbIDs []string
	if kbIDsStr := strings.TrimSpace(c.Query("knowledge_base_ids")); kbIDsStr != "" {
		for _, id := range strings.Split(kbIDsStr, ",") {
			if trimmed := strings.TrimSpace(id); trimmed != "" {
				kbIDs = append(kbIDs, trimmed)
			}
		}
	}

	var knowledgeIDs []string
	if kIDsStr := strings.TrimSpace(c.Query("knowledge_ids")); kIDsStr != "" {
		for _, id := range strings.Split(kIDsStr, ",") {
			if trimmed := strings.TrimSpace(id); trimmed != "" {
				knowledgeIDs = append(knowledgeIDs, trimmed)
			}
		}
	}

	var tagScopes []types.TagScope
	if raw := strings.TrimSpace(c.Query("tag_scopes")); raw != "" {
		if err := json.Unmarshal([]byte(raw), &tagScopes); err != nil {
			c.Error(errors.NewBadRequestError("tag_scopes must be valid JSON"))
			return
		}
	}

	// limit == 0 signals "unspecified" so the service falls back to the agent's
	// configured starter count. A provided value is passed through unchanged and
	// bounded by the service's safety cap.
	limit := 0
	if limitStr := c.Query("limit"); limitStr != "" {
		if parsed, err := strconv.Atoi(limitStr); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	logger.Infof(ctx, "Getting suggested questions for agent %s, kbIDs: %v, tagScopes: %d, limit: %d",
		secutils.SanitizeForLog(id), kbIDs, len(tagScopes), limit)

	questions, err := h.service.GetSuggestedQuestions(ctx, id, kbIDs, knowledgeIDs, tagScopes, limit)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"agent_id": id,
		})
		if err == service.ErrAgentNotFound {
			c.Error(errors.NewNotFoundError("Agent not found"))
			return
		}
		if appErr, ok := err.(*errors.AppError); ok {
			c.Error(appErr)
			return
		}
		// Reached only after the typed sentinels and *errors.AppError above, so
		// whatever lands here is a raw repository/driver error. Its text
		// (SQLSTATE, column names) must not reach the client; the full detail
		// is already logged above.
		_ = c.Error(errors.NewInternalServerError("Failed to build suggested questions"))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"questions": questions,
		},
	})
}

// validateAgentSandboxConfig rejects a selection the workspace does not have.
//
// Checking at save time is what makes the mistake fixable: a dangling reference
// only fails when the agent next runs a skill, mid-conversation, as an opaque
// resolution error with no hint about which agent to edit.
func (h *CustomAgentHandler) validateAgentSandboxConfig(
	ctx context.Context, cfg types.CustomAgentConfig,
) error {
	configID := strings.TrimSpace(cfg.SandboxConfigID)
	if h.desktop {
		if configID != "" {
			return errors.NewBadRequestError("Lite 不支持为智能体绑定沙箱配置")
		}
		return nil
	}
	if configID == "" || h.sandboxConfigs == nil {
		// Empty means the deployment-wide default, which always exists.
		return nil
	}
	tenantID, ok := types.TenantIDFromContext(ctx)
	if !ok {
		return errors.NewUnauthorizedError("Missing workspace context")
	}
	stored, err := h.sandboxConfigs.Get(ctx, tenantID, configID)
	if err != nil {
		return errors.NewInternalServerError("Failed to verify sandbox config").
			WithDetails(err.Error())
	}
	if stored == nil {
		return errors.NewBadRequestError("所选沙箱后端配置不存在，请重新选择")
	}
	return nil
}

// normalizeAgentReasoningEffort validates config.reasoning_effort and rewrites
// it to its canonical spelling.
//
// Without this the field was stored verbatim and cast straight to
// api.ReasoningEffort in agent/think.go and chat_pipeline/common.go. A typo
// there does not disable thinking, it silently enables it: ReasoningEffort
// wins over the legacy boolean and anything non-empty other than "off" counts
// as enabled, so `"reasoning_effort": "hgih"` turns thinking on for a vendor
// that then receives a level it rejects (or clamps in an unpredictable way).
func normalizeAgentReasoningEffort(cfg *types.CustomAgentConfig) error {
	if cfg == nil || cfg.ReasoningEffort == "" {
		return nil
	}
	level, ok := api.ParseReasoningEffort(cfg.ReasoningEffort)
	if !ok {
		return errors.NewBadRequestError(
			fmt.Sprintf("reasoning_effort must be one of %v", api.AllReasoningEfforts))
	}
	// Store the canonical value so EnsureDefaults and the editor never have to
	// know about the accepted aliases ("none", "true", ...).
	cfg.ReasoningEffort = string(level)
	return nil
}

func authorizeAgentKnowledgeScope(ctx context.Context, cfg types.CustomAgentConfig) error {
	scope, ok := types.TenantAPIKeyScopeFromContext(ctx)
	if !ok || !scope.IsKnowledgeBaseRestricted() {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(cfg.KBSelectionMode)) {
	case "none":
		return nil
	case "all":
		return errors.NewForbiddenError("API key scope does not allow agents that use all knowledge bases")
	case "selected":
		return types.AuthorizeTenantAPIKeyKnowledgeBases(ctx, cfg.KnowledgeBases...)
	default:
		if len(cfg.KnowledgeBases) == 0 {
			return nil
		}
		return types.AuthorizeTenantAPIKeyKnowledgeBases(ctx, cfg.KnowledgeBases...)
	}
}
