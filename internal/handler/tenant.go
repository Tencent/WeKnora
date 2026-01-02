package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/agent"
	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// TenantHandler 테넌트 관리를 위한 HTTP 요청 핸들러 구현
// REST API 엔드포인트를 통해 테넌트 생성, 조회, 업데이트 및 삭제 기능 제공
type TenantHandler struct {
	service     interfaces.TenantService
	userService interfaces.UserService
	config      *config.Config
}

// NewTenantHandler 제공된 서비스로 새로운 테넌트 핸들러 인스턴스 생성
// 매개변수:
//   - service: 비즈니스 로직을 위한 TenantService 인터페이스 구현체
//   - userService: 사용자 작업을 위한 UserService 인터페이스 구현체
//   - config: 애플리케이션 구성
//
// 반환값: 새로 생성된 TenantHandler에 대한 포인터
func NewTenantHandler(service interfaces.TenantService, userService interfaces.UserService, config *config.Config) *TenantHandler {
	return &TenantHandler{
		service:     service,
		userService: userService,
		config:      config,
	}
}

// CreateTenant godoc
// @Summary      테넌트 생성
// @Description  새로운 테넌트 생성
// @Tags         테넌트 관리
// @Accept       json
// @Produce      json
// @Param        request  body      types.Tenant  true  "테넌트 정보"
// @Success      201      {object}  map[string]interface{}  "생성된 테넌트"
// @Failure      400      {object}  errors.AppError         "요청 매개변수 오류"
// @Security     Bearer
// @Router       /tenants [post]
func (h *TenantHandler) CreateTenant(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start creating tenant")

	var tenantData types.Tenant
	if err := c.ShouldBindJSON(&tenantData); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		appErr := errors.NewValidationError("Invalid request parameters").WithDetails(err.Error())
		c.Error(appErr)
		return
	}

	logger.Infof(ctx, "Creating tenant, name: %s", secutils.SanitizeForLog(tenantData.Name))

	createdTenant, err := h.service.CreateTenant(ctx, &tenantData)
	if err != nil {
		// 애플리케이션 관련 오류인지 확인
		if appErr, ok := errors.IsAppError(err); ok {
			logger.Error(ctx, "Failed to create tenant: application error", appErr)
			c.Error(appErr)
		} else {
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(errors.NewInternalServerError("Failed to create tenant").WithDetails(err.Error()))
		}
		return
	}

	logger.Infof(
		ctx,
		"Tenant created successfully, ID: %d, name: %s",
		createdTenant.ID,
		secutils.SanitizeForLog(createdTenant.Name),
	)
	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data":    createdTenant,
	})
}

// GetTenant godoc
// @Summary      테넌트 상세 정보 조회
// @Description  ID를 기반으로 테넌트 상세 정보 조회
// @Tags         테넌트 관리
// @Accept       json
// @Produce      json
// @Param        id   path      int  true  "테넌트 ID"
// @Success      200  {object}  map[string]interface{}  "테넌트 상세 정보"
// @Failure      400  {object}  errors.AppError         "요청 매개변수 오류"
// @Failure      404  {object}  errors.AppError         "테넌트를 찾을 수 없음"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /tenants/{id} [get]
func (h *TenantHandler) GetTenant(c *gin.Context) {
	ctx := c.Request.Context()

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		logger.Errorf(ctx, "Invalid tenant ID: %s", secutils.SanitizeForLog(c.Param("id")))
		c.Error(errors.NewBadRequestError("Invalid tenant ID"))
		return
	}

	tenant, err := h.service.GetTenantByID(ctx, id)
	if err != nil {
		// 애플리케이션 관련 오류인지 확인
		if appErr, ok := errors.IsAppError(err); ok {
			logger.Error(ctx, "Failed to retrieve tenant: application error", appErr)
			c.Error(appErr)
		} else {
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(errors.NewInternalServerError("Failed to retrieve tenant").WithDetails(err.Error()))
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    tenant,
	})
}

// UpdateTenant godoc
// @Summary      테넌트 업데이트
// @Description  테넌트 정보 업데이트
// @Tags         테넌트 관리
// @Accept       json
// @Produce      json
// @Param        id       path      int           true  "테넌트 ID"
// @Param        request  body      types.Tenant  true  "테넌트 정보"
// @Success      200      {object}  map[string]interface{}  "업데이트된 테넌트"
// @Failure      400      {object}  errors.AppError         "요청 매개변수 오류"
// @Security     Bearer
// @Router       /tenants/{id} [put]
func (h *TenantHandler) UpdateTenant(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start updating tenant")

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		logger.Errorf(ctx, "Invalid tenant ID: %s", secutils.SanitizeForLog(c.Param("id")))
		c.Error(errors.NewBadRequestError("Invalid tenant ID"))
		return
	}

	var tenantData types.Tenant
	if err := c.ShouldBindJSON(&tenantData); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		c.Error(errors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}

	logger.Infof(ctx, "Updating tenant, ID: %d, Name: %s", id, secutils.SanitizeForLog(tenantData.Name))

	tenantData.ID = id
	updatedTenant, err := h.service.UpdateTenant(ctx, &tenantData)
	if err != nil {
		// 애플리케이션 관련 오류인지 확인
		if appErr, ok := errors.IsAppError(err); ok {
			logger.Error(ctx, "Failed to update tenant: application error", appErr)
			c.Error(appErr)
		} else {
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(errors.NewInternalServerError("Failed to update tenant").WithDetails(err.Error()))
		}
		return
	}

	logger.Infof(
		ctx,
		"Tenant updated successfully, ID: %d, Name: %s",
		updatedTenant.ID,
		secutils.SanitizeForLog(updatedTenant.Name),
	)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    updatedTenant,
	})
}

// DeleteTenant godoc
// @Summary      테넌트 삭제
// @Description  지정된 테넌트 삭제
// @Tags         테넌트 관리
// @Accept       json
// @Produce      json
// @Param        id   path      int  true  "테넌트 ID"
// @Success      200  {object}  map[string]interface{}  "삭제 성공"
// @Failure      400  {object}  errors.AppError         "요청 매개변수 오류"
// @Security     Bearer
// @Router       /tenants/{id} [delete]
func (h *TenantHandler) DeleteTenant(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start deleting tenant")

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		logger.Errorf(ctx, "Invalid tenant ID: %s", secutils.SanitizeForLog(c.Param("id")))
		c.Error(errors.NewBadRequestError("Invalid tenant ID"))
		return
	}

	logger.Infof(ctx, "Deleting tenant, ID: %d", id)

	if err := h.service.DeleteTenant(ctx, id); err != nil {
		// 애플리케이션 관련 오류인지 확인
		if appErr, ok := errors.IsAppError(err); ok {
			logger.Error(ctx, "Failed to delete tenant: application error", appErr)
			c.Error(appErr)
		} else {
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(errors.NewInternalServerError("Failed to delete tenant").WithDetails(err.Error()))
		}
		return
	}

	logger.Infof(ctx, "Tenant deleted successfully, ID: %d", id)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Tenant deleted successfully",
	})
}

// ListTenants godoc
// @Summary      테넌트 목록 조회
// @Description  현재 사용자가 액세스할 수 있는 테넌트 목록 조회
// @Tags         테넌트 관리
// @Accept       json
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "테넌트 목록"
// @Failure      500  {object}  errors.AppError         "서버 오류"
// @Security     Bearer
// @Router       /tenants [get]
func (h *TenantHandler) ListTenants(c *gin.Context) {
	ctx := c.Request.Context()

	tenants, err := h.service.ListTenants(ctx)
	if err != nil {
		// 애플리케이션 관련 오류인지 확인
		if appErr, ok := errors.IsAppError(err); ok {
			logger.Error(ctx, "Failed to retrieve tenant list: application error", appErr)
			c.Error(appErr)
		} else {
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(errors.NewInternalServerError("Failed to retrieve tenant list").WithDetails(err.Error()))
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"items": tenants,
		},
	})
}

// ListAllTenants godoc
// @Summary      모든 테넌트 목록 조회
// @Description  시스템의 모든 테넌트 조회 (크로스 테넌트 액세스 권한 필요)
// @Tags         테넌트 관리
// @Accept       json
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "모든 테넌트 목록"
// @Failure      403  {object}  errors.AppError         "권한 부족"
// @Security     Bearer
// @Router       /tenants/all [get]
func (h *TenantHandler) ListAllTenants(c *gin.Context) {
	ctx := c.Request.Context()

	// 컨텍스트에서 현재 사용자 가져오기
	user, err := h.userService.GetCurrentUser(ctx)
	if err != nil {
		logger.Errorf(ctx, "Failed to get current user: %v", err)
		c.Error(errors.NewUnauthorizedError("Failed to get user information").WithDetails(err.Error()))
		return
	}

	// 크로스 테넌트 액세스가 활성화되었는지 확인
	if h.config == nil || h.config.Tenant == nil || !h.config.Tenant.EnableCrossTenantAccess {
		logger.Warnf(ctx, "Cross-tenant access is disabled, user: %s", user.ID)
		c.Error(errors.NewForbiddenError("Cross-tenant access is disabled"))
		return
	}

	// 사용자가 권한을 가지고 있는지 확인
	if !user.CanAccessAllTenants {
		logger.Warnf(ctx, "User %s attempted to list all tenants without permission", user.ID)
		c.Error(errors.NewForbiddenError("Insufficient permissions to access all tenants"))
		return
	}

	tenants, err := h.service.ListAllTenants(ctx)
	if err != nil {
		// 애플리케이션 관련 오류인지 확인
		if appErr, ok := errors.IsAppError(err); ok {
			logger.Error(ctx, "Failed to retrieve all tenants list: application error", appErr)
			c.Error(appErr)
		} else {
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(errors.NewInternalServerError("Failed to retrieve all tenants list").WithDetails(err.Error()))
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"items": tenants,
		},
	})
}

// SearchTenants godoc
// @Summary      테넌트 검색
// @Description  테넌트 페이지별 검색 (크로스 테넌트 액세스 권한 필요)
// @Tags         테넌트 관리
// @Accept       json
// @Produce      json
// @Param        keyword    query     string  false  "검색 키워드"
// @Param        tenant_id  query     int     false  "테넌트 ID 필터"
// @Param        page       query     int     false  "페이지 번호"  default(1)
// @Param        page_size  query     int     false  "페이지당 항목 수"  default(20)
// @Success      200        {object}  map[string]interface{}  "검색 결과"
// @Failure      403        {object}  errors.AppError         "권한 부족"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /tenants/search [get]
func (h *TenantHandler) SearchTenants(c *gin.Context) {
	ctx := c.Request.Context()

	// 컨텍스트에서 현재 사용자 가져오기
	user, err := h.userService.GetCurrentUser(ctx)
	if err != nil {
		logger.Errorf(ctx, "Failed to get current user: %v", err)
		c.Error(errors.NewUnauthorizedError("Failed to get user information").WithDetails(err.Error()))
		return
	}

	// 크로스 테넌트 액세스가 활성화되었는지 확인
	if h.config == nil || h.config.Tenant == nil || !h.config.Tenant.EnableCrossTenantAccess {
		logger.Warnf(ctx, "Cross-tenant access is disabled, user: %s", user.ID)
		c.Error(errors.NewForbiddenError("Cross-tenant access is disabled"))
		return
	}

	// 사용자가 권한을 가지고 있는지 확인
	if !user.CanAccessAllTenants {
		logger.Warnf(ctx, "User %s attempted to search tenants without permission", user.ID)
		c.Error(errors.NewForbiddenError("Insufficient permissions to access all tenants"))
		return
	}

	// 쿼리 매개변수 파싱
	keyword := c.Query("keyword")
	tenantIDStr := c.Query("tenant_id")
	pageStr := c.DefaultQuery("page", "1")
	pageSizeStr := c.DefaultQuery("page_size", "20")

	var tenantID uint64
	if tenantIDStr != "" {
		parsedID, err := strconv.ParseUint(tenantIDStr, 10, 64)
		if err == nil {
			tenantID = parsedID
		}
	}

	page, err := strconv.Atoi(pageStr)
	if err != nil || page < 1 {
		page = 1
	}

	pageSize, err := strconv.Atoi(pageSizeStr)
	if err != nil || pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100 // 최대 페이지 크기 제한
	}

	tenants, total, err := h.service.SearchTenants(ctx, keyword, tenantID, page, pageSize)
	if err != nil {
		// 애플리케이션 관련 오류인지 확인
		if appErr, ok := errors.IsAppError(err); ok {
			logger.Error(ctx, "Failed to search tenants: application error", appErr)
			c.Error(appErr)
		} else {
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(errors.NewInternalServerError("Failed to search tenants").WithDetails(err.Error()))
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"items":     tenants,
			"total":     total,
			"page":      page,
			"page_size": pageSize,
		},
	})
}

// AgentConfigRequest 에이전트 구성을 업데이트하기 위한 요청 본문
type AgentConfigRequest struct {
	MaxIterations     int      `json:"max_iterations"`
	ReflectionEnabled bool     `json:"reflection_enabled"`
	AllowedTools      []string `json:"allowed_tools"`
	Temperature       float64  `json:"temperature"`
	SystemPrompt      string   `json:"system_prompt,omitempty"` // 통합 시스템 프롬프트 ({{web_search_status}} 플레이스홀더 사용)
}

// GetTenantAgentConfig godoc
// @Summary      테넌트 에이전트 구성 조회
// @Description  테넌트의 전역 에이전트 구성 조회 (모든 세션에 기본적으로 적용됨)
// @Tags         테넌트 관리
// @Accept       json
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "에이전트 구성"
// @Failure      400  {object}  errors.AppError         "요청 매개변수 오류"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /tenants/kv/agent-config [get]
func (h *TenantHandler) GetTenantAgentConfig(c *gin.Context) {
	ctx := c.Request.Context()
	tenant := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
	if tenant == nil {
		logger.Error(ctx, "Tenant is empty")
		c.Error(errors.NewBadRequestError("Tenant is empty"))
		return
	}
	// tools 패키지에서 사용 가능한 도구 목록 수집
	availableTools := make([]gin.H, 0)
	for _, t := range agenttools.AvailableToolDefinitions() {
		availableTools = append(availableTools, gin.H{
			"name":        t.Name,
			"label":       t.Label,
			"description": t.Description,
		})
	}

	// agent 패키지에서 플레이스홀더 정의 가져오기
	availablePlaceholders := make([]gin.H, 0)
	for _, p := range agent.AvailablePlaceholders() {
		availablePlaceholders = append(availablePlaceholders, gin.H{
			"name":        p.Name,
			"label":       p.Label,
			"description": p.Description,
		})
	}
	if tenant.AgentConfig == nil {
		// 설정되지 않은 경우 기본 구성 반환
		logger.Info(ctx, "Tenant has no agent config, returning defaults")

		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data": gin.H{
				"max_iterations":           agent.DefaultAgentMaxIterations,
				"reflection_enabled":       agent.DefaultAgentReflectionEnabled,
				"allowed_tools":            agenttools.DefaultAllowedTools(),
				"temperature":              agent.DefaultAgentTemperature,
				"system_prompt":            agent.ProgressiveRAGSystemPrompt,
				"use_custom_system_prompt": false,
				"available_tools":          availableTools,
				"available_placeholders":   availablePlaceholders,
			},
		})
		return
	}

	// 시스템 프롬프트 가져오기, 비어 있으면 기본값 사용
	systemPrompt := tenant.AgentConfig.ResolveSystemPrompt(true) // 통합 프롬프트에서는 webSearchEnabled가 중요하지 않음
	if systemPrompt == "" {
		systemPrompt = agent.ProgressiveRAGSystemPrompt
	}

	logger.Infof(ctx, "Retrieved tenant agent config successfully, Tenant ID: %d", tenant.ID)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"max_iterations":           tenant.AgentConfig.MaxIterations,
			"reflection_enabled":       tenant.AgentConfig.ReflectionEnabled,
			"allowed_tools":            agenttools.DefaultAllowedTools(),
			"temperature":              tenant.AgentConfig.Temperature,
			"system_prompt":            systemPrompt,
			"use_custom_system_prompt": tenant.AgentConfig.UseCustomSystemPrompt,
			"available_tools":          availableTools,
			"available_placeholders":   availablePlaceholders,
		},
	})
}

// updateTenantAgentConfigInternal 테넌트의 에이전트 구성 업데이트
// 이 테넌트의 모든 세션에 대한 전역 에이전트 구성 설정
func (h *TenantHandler) updateTenantAgentConfigInternal(c *gin.Context) {
	ctx := c.Request.Context()
	logger.Info(ctx, "Start updating tenant agent config")
	var req AgentConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		c.Error(errors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}

	// 구성 검증
	if req.MaxIterations <= 0 || req.MaxIterations > 30 {
		c.Error(errors.NewAgentInvalidMaxIterationsError())
		return
	}
	if req.Temperature < 0 || req.Temperature > 2 {
		c.Error(errors.NewAgentInvalidTemperatureError())
		return
	}

	// 기존 테넌트 가져오기
	tenant := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
	if tenant == nil {
		logger.Error(ctx, "Tenant is empty")
		c.Error(errors.NewBadRequestError("Tenant is empty"))
		return
	}
	// 에이전트 구성 업데이트
	// 사용자 정의 프롬프트가 설정되어 있는지 여부에 따라 사용자 정의 프롬프트 사용 여부 결정
	// 새로운 통합 SystemPrompt와 더 이상 사용되지 않는 개별 프롬프트 모두 지원
	systemPrompt := req.SystemPrompt
	useCustomPrompt := systemPrompt != ""

	agentConfig := &types.AgentConfig{
		MaxIterations:         req.MaxIterations,
		ReflectionEnabled:     req.ReflectionEnabled,
		AllowedTools:          agenttools.DefaultAllowedTools(),
		Temperature:           req.Temperature,
		SystemPrompt:          systemPrompt,
		UseCustomSystemPrompt: useCustomPrompt,
	}

	_, err := h.service.UpdateTenant(ctx, tenant)
	if err != nil {
		if appErr, ok := errors.IsAppError(err); ok {
			logger.Error(ctx, "Failed to update tenant: application error", appErr)
			c.Error(appErr)
		} else {
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(errors.NewInternalServerError("Failed to update tenant agent config").WithDetails(err.Error()))
		}
		return
	}

	logger.Infof(ctx, "Tenant agent config updated successfully, Tenant ID: %d", tenant.ID)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    agentConfig,
		"message": "Agent configuration updated successfully",
	})
}

// GetTenantKV godoc
// @Summary      테넌트 KV 구성 조회
// @Description  테넌트 수준의 KV 구성 조회 (agent-config, web-search-config, conversation-config 지원)
// @Tags         테넌트 관리
// @Accept       json
// @Produce      json
// @Param        key  path      string  true  "구성 키 이름"
// @Success      200  {object}  map[string]interface{}  "구성 값"
// @Failure      400  {object}  errors.AppError         "지원되지 않는 키"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /tenants/kv/{key} [get]
func (h *TenantHandler) GetTenantKV(c *gin.Context) {
	ctx := c.Request.Context()
	key := secutils.SanitizeForLog(c.Param("key"))

	switch key {
	case "agent-config":
		h.GetTenantAgentConfig(c)
		return
	case "web-search-config":
		h.GetTenantWebSearchConfig(c)
		return
	case "conversation-config":
		h.GetTenantConversationConfig(c)
		return
	case "prompt-templates":
		h.GetPromptTemplates(c)
		return
	default:
		logger.Info(ctx, "KV key not supported", "key", key)
		c.Error(errors.NewBadRequestError("unsupported key"))
		return
	}
}

// UpdateTenantKV godoc
// @Summary      테넌트 KV 구성 업데이트
// @Description  테넌트 수준의 KV 구성 업데이트 (agent-config, web-search-config, conversation-config 지원)
// @Tags         테넌트 관리
// @Accept       json
// @Produce      json
// @Param        key      path      string  true  "구성 키 이름"
// @Param        request  body      object  true  "구성 값"
// @Success      200      {object}  map[string]interface{}  "업데이트 성공"
// @Failure      400      {object}  errors.AppError         "지원되지 않는 키"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /tenants/kv/{key} [put]
func (h *TenantHandler) UpdateTenantKV(c *gin.Context) {
	ctx := c.Request.Context()
	key := secutils.SanitizeForLog(c.Param("key"))

	switch key {
	case "agent-config":
		h.updateTenantAgentConfigInternal(c)
		return
	case "web-search-config":
		h.updateTenantWebSearchConfigInternal(c)
		return
	case "conversation-config":
		h.updateTenantConversationInternal(c)
		return
	default:
		logger.Info(ctx, "KV key not supported", "key", key)
		c.Error(errors.NewBadRequestError("unsupported key"))
		return
	}
}

// updateTenantWebSearchConfigInternal 테넌트의 웹 검색 구성 업데이트
func (h *TenantHandler) updateTenantWebSearchConfigInternal(c *gin.Context) {
	ctx := c.Request.Context()

	// 강력한 타입의 구조체에 직접 바인딩
	var cfg types.WebSearchConfig
	if err := c.ShouldBindJSON(&cfg); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		c.Error(errors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}

	// 구성 검증
	if cfg.MaxResults < 1 || cfg.MaxResults > 50 {
		c.Error(errors.NewBadRequestError("max_results must be between 1 and 50"))
		return
	}

	tenant := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
	if tenant == nil {
		logger.Error(ctx, "Tenant is empty")
		c.Error(errors.NewBadRequestError("Tenant is empty"))
		return
	}

	tenant.WebSearchConfig = &cfg
	updatedTenant, err := h.service.UpdateTenant(ctx, tenant)
	if err != nil {
		if appErr, ok := errors.IsAppError(err); ok {
			logger.Error(ctx, "Failed to update tenant: application error", appErr)
			c.Error(appErr)
		} else {
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(errors.NewInternalServerError("Failed to update tenant web search config").WithDetails(err.Error()))
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    updatedTenant.WebSearchConfig,
		"message": "Web search configuration updated successfully",
	})
}

// GetTenantWebSearchConfig godoc
// @Summary      테넌트 웹 검색 구성 조회
// @Description  테넌트의 웹 검색 구성 조회
// @Tags         테넌트 관리
// @Accept       json
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "웹 검색 구성"
// @Failure      400  {object}  errors.AppError         "요청 매개변수 오류"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /tenants/kv/web-search-config [get]
func (h *TenantHandler) GetTenantWebSearchConfig(c *gin.Context) {
	ctx := c.Request.Context()
	logger.Info(ctx, "Start getting tenant web search config")
	// 테넌트 가져오기
	tenant := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
	if tenant == nil {
		logger.Error(ctx, "Tenant is empty")
		c.Error(errors.NewBadRequestError("Tenant is empty"))
		return
	}

	logger.Infof(ctx, "Tenant web search config retrieved successfully, Tenant ID: %d", tenant.ID)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    tenant.WebSearchConfig,
	})
}

func (h *TenantHandler) buildDefaultConversationConfig() *types.ConversationConfig {
	return &types.ConversationConfig{
		Prompt:               h.config.Conversation.Summary.Prompt,
		ContextTemplate:      h.config.Conversation.Summary.ContextTemplate,
		Temperature:          h.config.Conversation.Summary.Temperature,
		MaxCompletionTokens:  h.config.Conversation.Summary.MaxCompletionTokens,
		MaxRounds:            h.config.Conversation.MaxRounds,
		EmbeddingTopK:        h.config.Conversation.EmbeddingTopK,
		KeywordThreshold:     h.config.Conversation.KeywordThreshold,
		VectorThreshold:      h.config.Conversation.VectorThreshold,
		RerankTopK:           h.config.Conversation.RerankTopK,
		RerankThreshold:      h.config.Conversation.RerankThreshold,
		EnableRewrite:        h.config.Conversation.EnableRewrite,
		EnableQueryExpansion: h.config.Conversation.EnableQueryExpansion,
		FallbackStrategy:     h.config.Conversation.FallbackStrategy,
		FallbackResponse:     h.config.Conversation.FallbackResponse,
		FallbackPrompt:       h.config.Conversation.FallbackPrompt,
		RewritePromptUser:    h.config.Conversation.RewritePromptUser,
		RewritePromptSystem:  h.config.Conversation.RewritePromptSystem,
	}
}

func validateConversationConfig(req *types.ConversationConfig) error {
	if req.MaxRounds <= 0 {
		return errors.NewBadRequestError("max_rounds must be greater than 0")
	}
	if req.EmbeddingTopK <= 0 {
		return errors.NewBadRequestError("embedding_top_k must be greater than 0")
	}
	if req.KeywordThreshold < 0 || req.KeywordThreshold > 1 {
		return errors.NewBadRequestError("keyword_threshold must be between 0 and 1")
	}
	if req.VectorThreshold < 0 || req.VectorThreshold > 1 {
		return errors.NewBadRequestError("vector_threshold must be between 0 and 1")
	}
	if req.RerankTopK <= 0 {
		return errors.NewBadRequestError("rerank_top_k must be greater than 0")
	}
	if req.RerankThreshold < 0 || req.RerankThreshold > 1 {
		return errors.NewBadRequestError("rerank_threshold must be between 0 and 1")
	}
	if req.Temperature < 0 || req.Temperature > 2 {
		return errors.NewBadRequestError("temperature must be between 0 and 2")
	}
	if req.MaxCompletionTokens <= 0 || req.MaxCompletionTokens > 100000 {
		return errors.NewBadRequestError("max_completion_tokens must be between 1 and 100000")
	}
	if req.FallbackStrategy != "" &&
		req.FallbackStrategy != string(types.FallbackStrategyFixed) &&
		req.FallbackStrategy != string(types.FallbackStrategyModel) {
		return errors.NewBadRequestError("fallback_strategy is invalid")
	}
	return nil
}

// GetTenantConversationConfig godoc
// @Summary      테넌트 대화 구성 조회
// @Description  테넌트의 전역 대화 구성 조회 (일반 모드 세션에 기본적으로 적용됨)
// @Tags         테넌트 관리
// @Accept       json
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "대화 구성"
// @Failure      400  {object}  errors.AppError         "요청 매개변수 오류"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /tenants/kv/conversation-config [get]
func (h *TenantHandler) GetTenantConversationConfig(c *gin.Context) {
	ctx := c.Request.Context()
	tenant := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
	if tenant == nil {
		logger.Error(ctx, "Tenant is empty")
		c.Error(errors.NewBadRequestError("Tenant is empty"))
		return
	}

	// 테넌트에 대화 구성이 없는 경우 config.yaml의 기본값 반환
	var response *types.ConversationConfig
	logger.Info(ctx, "Tenant has no conversation config, returning defaults")
	response = h.buildDefaultConversationConfig()
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    response,
	})
}

// updateTenantConversationInternal 테넌트의 대화 구성 업데이트
// 이 테넌트의 일반 모드 세션에 대한 전역 대화 구성 설정
func (h *TenantHandler) updateTenantConversationInternal(c *gin.Context) {
	ctx := c.Request.Context()
	logger.Info(ctx, "Start updating tenant conversation config")

	var req types.ConversationConfig
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		c.Error(errors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}

	// 구성 검증
	if err := validateConversationConfig(&req); err != nil {
		c.Error(err)
		return
	}

	// 기존 테넌트 가져오기
	tenant := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
	if tenant == nil {
		logger.Error(ctx, "Tenant is empty")
		c.Error(errors.NewBadRequestError("Tenant is empty"))
		return
	}

	// 대화 구성 업데이트
	tenant.ConversationConfig = &req

	updatedTenant, err := h.service.UpdateTenant(ctx, tenant)
	if err != nil {
		if appErr, ok := errors.IsAppError(err); ok {
			logger.Error(ctx, "Failed to update tenant: application error", appErr)
			c.Error(appErr)
		} else {
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(errors.NewInternalServerError("Failed to update tenant conversation config").WithDetails(err.Error()))
		}
		return
	}

	logger.Infof(ctx, "Tenant conversation config updated successfully, Tenant ID: %d", tenant.ID)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    updatedTenant.ConversationConfig,
		"message": "Conversation configuration updated successfully",
	})
}

// GetPromptTemplates godoc
// @Summary      프롬프트 템플릿 조회
// @Description  시스템 구성된 프롬프트 템플릿 목록 조회
// @Tags         테넌트 관리
// @Accept       json
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "프롬프트 템플릿 구성"
// @Failure      400  {object}  errors.AppError         "요청 매개변수 오류"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /tenants/kv/prompt-templates [get]
func (h *TenantHandler) GetPromptTemplates(c *gin.Context) {
	// config.yaml에서 프롬프트 템플릿 반환
	templates := h.config.PromptTemplates
	if templates == nil {
		templates = &config.PromptTemplatesConfig{}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    templates,
	})
}
