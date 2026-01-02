package handler

import (
	"net/http"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
)

// CustomAgentHandler 사용자 정의 에이전트 작업을 위한 HTTP 핸들러 정의
type CustomAgentHandler struct {
	service interfaces.CustomAgentService
}

// NewCustomAgentHandler 새로운 사용자 정의 에이전트 핸들러 인스턴스 생성
func NewCustomAgentHandler(service interfaces.CustomAgentService) *CustomAgentHandler {
	return &CustomAgentHandler{
		service: service,
	}
}

// CreateAgentRequest 에이전트 생성을 위한 요청 본문 정의
type CreateAgentRequest struct {
	Name        string                   `json:"name" binding:"required"`
	Description string                   `json:"description"`
	Avatar      string                   `json:"avatar"`
	Config      types.CustomAgentConfig  `json:"config"`
}

// UpdateAgentRequest 에이전트 업데이트를 위한 요청 본문 정의
type UpdateAgentRequest struct {
	Name        string                   `json:"name"`
	Description string                   `json:"description"`
	Avatar      string                   `json:"avatar"`
	Config      types.CustomAgentConfig  `json:"config"`
}

// CreateAgent godoc
// @Summary      에이전트 생성
// @Description  새로운 사용자 정의 에이전트 생성
// @Tags         에이전트
// @Accept       json
// @Produce      json
// @Param        request  body      CreateAgentRequest  true  "에이전트 정보"
// @Success      201      {object}  map[string]interface{}  "생성된 에이전트"
// @Failure      400      {object}  errors.AppError         "요청 매개변수 오류"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /agents [post]
func (h *CustomAgentHandler) CreateAgent(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start creating custom agent")

	// 요청 본문 파싱
	var req CreateAgentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		c.Error(errors.NewBadRequestError("Invalid request parameters").WithDetails(err.Error()))
		return
	}

	// 에이전트 객체 생성
	agent := &types.CustomAgent{
		Name:        req.Name,
		Description: req.Description,
		Avatar:      req.Avatar,
		Config:      req.Config,
	}

	logger.Infof(ctx, "Creating custom agent, name: %s, agent_mode: %s",
		secutils.SanitizeForLog(req.Name), req.Config.AgentMode)

	// 서비스를 사용하여 에이전트 생성
	createdAgent, err := h.service.CreateAgent(ctx, agent)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		if err == service.ErrAgentNameRequired {
			c.Error(errors.NewBadRequestError(err.Error()))
			return
		}
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(ctx, "Custom agent created successfully, ID: %s, name: %s",
		secutils.SanitizeForLog(createdAgent.ID), secutils.SanitizeForLog(createdAgent.Name))
	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data":    createdAgent,
	})
}

// GetAgent godoc
// @Summary      에이전트 상세 정보 조회
// @Description  ID를 기반으로 에이전트 상세 정보 조회
// @Tags         에이전트
// @Accept       json
// @Produce      json
// @Param        id   path      string  true  "에이전트 ID"
// @Success      200  {object}  map[string]interface{}  "에이전트 상세 정보"
// @Failure      400  {object}  errors.AppError         "요청 매개변수 오류"
// @Failure      404  {object}  errors.AppError         "에이전트를 찾을 수 없음"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /agents/{id} [get]
func (h *CustomAgentHandler) GetAgent(c *gin.Context) {
	ctx := c.Request.Context()

	// URL 매개변수에서 에이전트 ID 가져오기
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
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    agent,
	})
}

// ListAgents godoc
// @Summary      에이전트 목록 조회
// @Description  현재 테넌트의 모든 에이전트(내장 에이전트 포함) 조회
// @Tags         에이전트
// @Accept       json
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "에이전트 목록"
// @Failure      500  {object}  errors.AppError         "서버 오류"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /agents [get]
func (h *CustomAgentHandler) ListAgents(c *gin.Context) {
	ctx := c.Request.Context()

	// 현재 테넌트의 모든 에이전트 가져오기
	agents, err := h.service.ListAgents(ctx)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    agents,
	})
}

// UpdateAgent godoc
// @Summary      에이전트 업데이트
// @Description  에이전트의 이름, 설명 및 구성 업데이트
// @Tags         에이전트
// @Accept       json
// @Produce      json
// @Param        id       path      string              true  "에이전트 ID"
// @Param        request  body      UpdateAgentRequest  true  "업데이트 요청"
// @Success      200      {object}  map[string]interface{}  "업데이트된 에이전트"
// @Failure      400      {object}  errors.AppError         "요청 매개변수 오류"
// @Failure      403      {object}  errors.AppError         "내장 에이전트는 수정할 수 없음"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /agents/{id} [put]
func (h *CustomAgentHandler) UpdateAgent(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start updating custom agent")

	// URL 매개변수에서 에이전트 ID 가져오기
	id := secutils.SanitizeForLog(c.Param("id"))
	if id == "" {
		logger.Error(ctx, "Agent ID is empty")
		c.Error(errors.NewBadRequestError("Agent ID cannot be empty"))
		return
	}

	// 요청 본문 파싱
	var req UpdateAgentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		c.Error(errors.NewBadRequestError("Invalid request parameters").WithDetails(err.Error()))
		return
	}

	// 에이전트 객체 생성
	agent := &types.CustomAgent{
		ID:          id,
		Name:        req.Name,
		Description: req.Description,
		Avatar:      req.Avatar,
		Config:      req.Config,
	}

	logger.Infof(ctx, "Updating custom agent, ID: %s, name: %s",
		secutils.SanitizeForLog(id), secutils.SanitizeForLog(req.Name))

	// 에이전트 업데이트
	updatedAgent, err := h.service.UpdateAgent(ctx, agent)
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
		default:
			c.Error(errors.NewInternalServerError(err.Error()))
		}
		return
	}

	logger.Infof(ctx, "Custom agent updated successfully, ID: %s", secutils.SanitizeForLog(id))
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    updatedAgent,
	})
}

// DeleteAgent godoc
// @Summary      에이전트 삭제
// @Description  지정된 에이전트 삭제
// @Tags         에이전트
// @Accept       json
// @Produce      json
// @Param        id   path      string  true  "에이전트 ID"
// @Success      200  {object}  map[string]interface{}  "삭제 성공"
// @Failure      400  {object}  errors.AppError         "요청 매개변수 오류"
// @Failure      403  {object}  errors.AppError         "내장 에이전트는 삭제할 수 없음"
// @Failure      404  {object}  errors.AppError         "에이전트를 찾을 수 없음"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /agents/{id} [delete]
func (h *CustomAgentHandler) DeleteAgent(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start deleting custom agent")

	// URL 매개변수에서 에이전트 ID 가져오기
	id := secutils.SanitizeForLog(c.Param("id"))
	if id == "" {
		logger.Error(ctx, "Agent ID is empty")
		c.Error(errors.NewBadRequestError("Agent ID cannot be empty"))
		return
	}

	logger.Infof(ctx, "Deleting custom agent, ID: %s", secutils.SanitizeForLog(id))

	// 에이전트 삭제
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
			c.Error(errors.NewInternalServerError(err.Error()))
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
// @Summary      에이전트 복사
// @Description  지정된 에이전트 복사
// @Tags         에이전트
// @Accept       json
// @Produce      json
// @Param        id   path      string  true  "에이전트 ID"
// @Success      201  {object}  map[string]interface{}  "복사 성공"
// @Failure      400  {object}  errors.AppError         "요청 매개변수 오류"
// @Failure      404  {object}  errors.AppError         "에이전트를 찾을 수 없음"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /agents/{id}/copy [post]
func (h *CustomAgentHandler) CopyAgent(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start copying custom agent")

	// URL 매개변수에서 에이전트 ID 가져오기
	id := secutils.SanitizeForLog(c.Param("id"))
	if id == "" {
		logger.Error(ctx, "Agent ID is empty")
		c.Error(errors.NewBadRequestError("Agent ID cannot be empty"))
		return
	}

	logger.Infof(ctx, "Copying custom agent, ID: %s", secutils.SanitizeForLog(id))

	// 에이전트 복사
	copiedAgent, err := h.service.CopyAgent(ctx, id)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"agent_id": id,
		})
		switch err {
		case service.ErrAgentNotFound:
			c.Error(errors.NewNotFoundError("Agent not found"))
		default:
			c.Error(errors.NewInternalServerError(err.Error()))
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
// @Summary      플레이스홀더 정의 조회
// @Description  필드 유형별로 그룹화된 모든 사용 가능한 프롬프트 플레이스홀더 정의 조회
// @Tags         에이전트
// @Accept       json
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "플레이스홀더 정의"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /agents/placeholders [get]
func (h *CustomAgentHandler) GetPlaceholders(c *gin.Context) {
	// 필드 유형별로 그룹화된 모든 플레이스홀더 정의 반환
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
