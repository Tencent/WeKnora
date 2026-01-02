package session

import (
	"net/http"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
)

// Handler 대화 세션과 관련된 모든 HTTP 요청을 처리합니다.
type Handler struct {
	messageService       interfaces.MessageService       // 메시지 관리 서비스
	sessionService       interfaces.SessionService       // 세션 관리 서비스
	streamManager        interfaces.StreamManager        // 스트리밍 응답 관리자
	config               *config.Config                  // 애플리케이션 구성
	knowledgebaseService interfaces.KnowledgeBaseService // 지식베이스 관리 서비스
	customAgentService   interfaces.CustomAgentService   // 사용자 정의 에이전트 관리 서비스
}

// NewHandler 필요한 모든 종속성을 가진 Handler의 새 인스턴스를 생성합니다.
func NewHandler(
	sessionService interfaces.SessionService,
	messageService interfaces.MessageService,
	streamManager interfaces.StreamManager,
	config *config.Config,
	knowledgebaseService interfaces.KnowledgeBaseService,
	customAgentService interfaces.CustomAgentService,
) *Handler {
	return &Handler{
		sessionService:       sessionService,
		messageService:       messageService,
		streamManager:        streamManager,
		config:               config,
		knowledgebaseService: knowledgebaseService,
		customAgentService:   customAgentService,
	}
}

// CreateSession godoc
// @Summary      세션 생성
// @Description  새로운 대화 세션 생성
// @Tags         세션
// @Accept       json
// @Produce      json
// @Param        request  body      CreateSessionRequest  true  "세션 생성 요청"
// @Success      201      {object}  map[string]interface{}  "생성된 세션"
// @Failure      400      {object}  errors.AppError         "요청 매개변수 오류"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /sessions [post]
func (h *Handler) CreateSession(c *gin.Context) {
	ctx := c.Request.Context()
	// 요청 본문 파싱 및 검증
	var request CreateSessionRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		logger.Error(ctx, "Failed to validate session creation parameters", err)
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	// 컨텍스트에서 테넌트 ID 가져오기
	tenantID, exists := c.Get(types.TenantIDContextKey.String())
	if !exists {
		logger.Error(ctx, "Failed to get tenant ID")
		c.Error(errors.NewUnauthorizedError("Unauthorized"))
		return
	}

	// 세션은 이제 지식베이스와 독립적입니다:
	// - 모든 구성은 쿼리 시점에 사용자 정의 에이전트에서 가져옵니다
	// - 세션은 기본 정보(테넌트 ID, 제목, 설명)만 저장합니다
	logger.Infof(
		ctx,
		"Processing session creation request, tenant ID: %d",
		tenantID,
	)

	// 기본 속성으로 세션 객체 생성
	createdSession := &types.Session{
		TenantID:    tenantID.(uint64),
		Title:       request.Title,
		Description: request.Description,
	}

	// 세션 생성 서비스 호출
	logger.Infof(ctx, "Calling session service to create session")
	createdSession, err := h.sessionService.CreateSession(ctx, createdSession)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	// 생성된 세션 반환
	logger.Infof(ctx, "Session created successfully, ID: %s", createdSession.ID)
	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data":    createdSession,
	})
}

// GetSession godoc
// @Summary      세션 상세 정보 조회
// @Description  ID를 기반으로 세션 상세 정보 조회
// @Tags         세션
// @Accept       json
// @Produce      json
// @Param        id   path      string  true  "세션 ID"
// @Success      200  {object}  map[string]interface{}  "세션 상세 정보"
// @Failure      404  {object}  errors.AppError         "세션을 찾을 수 없음"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /sessions/{id} [get]
func (h *Handler) GetSession(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start retrieving session")

	// URL 매개변수에서 세션 ID 가져오기
	id := secutils.SanitizeForLog(c.Param("id"))
	if id == "" {
		logger.Error(ctx, "Session ID is empty")
		c.Error(errors.NewBadRequestError(errors.ErrInvalidSessionID.Error()))
		return
	}

	// 세션 상세 정보 조회 서비스 호출
	logger.Infof(ctx, "Retrieving session, ID: %s", id)
	session, err := h.sessionService.GetSession(ctx, id)
	if err != nil {
		if err == errors.ErrSessionNotFound {
			logger.Warnf(ctx, "Session not found, ID: %s", id)
			c.Error(errors.NewNotFoundError(err.Error()))
			return
		}
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	// 세션 데이터 반환
	logger.Infof(ctx, "Session retrieved successfully, ID: %s", id)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    session,
	})
}

// GetSessionsByTenant godoc
// @Summary      세션 목록 조회
// @Description  현재 테넌트의 세션 목록 조회, 페이징 지원
// @Tags         세션
// @Accept       json
// @Produce      json
// @Param        page       query     int  false  "페이지 번호"
// @Param        page_size  query     int  false  "페이지당 항목 수"
// @Success      200        {object}  map[string]interface{}  "세션 목록"
// @Failure      400        {object}  errors.AppError         "요청 매개변수 오류"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /sessions [get]
func (h *Handler) GetSessionsByTenant(c *gin.Context) {
	ctx := c.Request.Context()

	// 쿼리에서 페이징 매개변수 파싱
	var pagination types.Pagination
	if err := c.ShouldBindQuery(&pagination); err != nil {
		logger.Error(ctx, "Failed to parse pagination parameters", err)
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	// 페이징된 쿼리를 사용하여 세션 가져오기
	result, err := h.sessionService.GetPagedSessionsByTenant(ctx, &pagination)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	// 페이징 데이터와 함께 세션 반환
	c.JSON(http.StatusOK, gin.H{
		"success":   true,
		"data":      result.Data,
		"total":     result.Total,
		"page":      result.Page,
		"page_size": result.PageSize,
	})
}

// UpdateSession godoc
// @Summary      세션 업데이트
// @Description  세션 속성 업데이트
// @Tags         세션
// @Accept       json
// @Produce      json
// @Param        id       path      string         true  "세션 ID"
// @Param        request  body      types.Session  true  "세션 정보"
// @Success      200      {object}  map[string]interface{}  "업데이트된 세션"
// @Failure      404      {object}  errors.AppError         "세션을 찾을 수 없음"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /sessions/{id} [put]
func (h *Handler) UpdateSession(c *gin.Context) {
	ctx := c.Request.Context()

	// URL 매개변수에서 세션 ID 가져오기
	id := secutils.SanitizeForLog(c.Param("id"))
	if id == "" {
		logger.Error(ctx, "Session ID is empty")
		c.Error(errors.NewBadRequestError(errors.ErrInvalidSessionID.Error()))
		return
	}

	// 권한 부여를 위해 컨텍스트에서 테넌트 ID 검증
	tenantID, exists := c.Get(types.TenantIDContextKey.String())
	if !exists {
		logger.Error(ctx, "Failed to get tenant ID")
		c.Error(errors.NewUnauthorizedError("Unauthorized"))
		return
	}

	// 요청 본문을 세션 객체로 파싱
	var session types.Session
	if err := c.ShouldBindJSON(&session); err != nil {
		logger.Error(ctx, "Failed to parse session data", err)
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	session.ID = id
	session.TenantID = tenantID.(uint64)

	// 세션 업데이트 서비스 호출
	if err := h.sessionService.UpdateSession(ctx, &session); err != nil {
		if err == errors.ErrSessionNotFound {
			logger.Warnf(ctx, "Session not found, ID: %s", id)
			c.Error(errors.NewNotFoundError(err.Error()))
			return
		}
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	// 업데이트된 세션 반환
	logger.Infof(ctx, "Session updated successfully, ID: %s", id)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    session,
	})
}

// DeleteSession godoc
// @Summary      세션 삭제
// @Description  지정된 세션 삭제
// @Tags         세션
// @Accept       json
// @Produce      json
// @Param        id   path      string  true  "세션 ID"
// @Success      200  {object}  map[string]interface{}  "삭제 성공"
// @Failure      404  {object}  errors.AppError         "세션을 찾을 수 없음"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /sessions/{id} [delete]
func (h *Handler) DeleteSession(c *gin.Context) {
	ctx := c.Request.Context()

	// URL 매개변수에서 세션 ID 가져오기
	id := secutils.SanitizeForLog(c.Param("id"))
	if id == "" {
		logger.Error(ctx, "Session ID is empty")
		c.Error(errors.NewBadRequestError(errors.ErrInvalidSessionID.Error()))
		return
	}

	// 세션 삭제 서비스 호출
	if err := h.sessionService.DeleteSession(ctx, id); err != nil {
		if err == errors.ErrSessionNotFound {
			logger.Warnf(ctx, "Session not found, ID: %s", id)
			c.Error(errors.NewNotFoundError(err.Error()))
			return
		}
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	// 성공 메시지 반환
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Session deleted successfully",
	})
}
