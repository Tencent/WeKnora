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

// ChunkHandler 청크 작업을 위한 HTTP 핸들러 정의
type ChunkHandler struct {
	service interfaces.ChunkService
}

// NewChunkHandler 새로운 청크 핸들러 생성
func NewChunkHandler(service interfaces.ChunkService) *ChunkHandler {
	return &ChunkHandler{service: service}
}

// GetChunkByIDOnly godoc
// @Summary      ID로 청크 조회
// @Description  청크 ID로만 청크 상세 정보 조회 (knowledge_id 필요 없음)
// @Tags         청크 관리
// @Accept       json
// @Produce      json
// @Param        id   path      string  true  "청크 ID"
// @Success      200  {object}  map[string]interface{}  "청크 상세 정보"
// @Failure      400  {object}  errors.AppError         "요청 매개변수 오류"
// @Failure      404  {object}  errors.AppError         "청크를 찾을 수 없음"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /chunks/by-id/{id} [get]
func (h *ChunkHandler) GetChunkByIDOnly(c *gin.Context) {
	ctx := c.Request.Context()
	logger.Info(ctx, "Start retrieving chunk by ID only")

	chunkID := secutils.SanitizeForLog(c.Param("id"))
	if chunkID == "" {
		logger.Error(ctx, "Chunk ID is empty")
		c.Error(errors.NewBadRequestError("Chunk ID cannot be empty"))
		return
	}

	// 컨텍스트에서 테넌트 ID 가져오기
	tenantID, exists := c.Get(types.TenantIDContextKey.String())
	if !exists {
		logger.Error(ctx, "Failed to get tenant ID")
		c.Error(errors.NewUnauthorizedError("Unauthorized"))
		return
	}

	logger.Infof(ctx, "Retrieving chunk by ID, chunk ID: %s, tenant ID: %d", chunkID, tenantID)

	// ID로 청크 가져오기
	chunk, err := h.service.GetChunkByID(ctx, chunkID)
	if err != nil {
		if err == service.ErrChunkNotFound {
			logger.Warnf(ctx, "Chunk not found, chunk ID: %s", chunkID)
			c.Error(errors.NewNotFoundError("Chunk not found"))
			return
		}
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	// 테넌트 ID 검증
	if chunk.TenantID != tenantID.(uint64) {
		logger.Warnf(
			ctx,
			"Tenant has no permission to access chunk, chunk ID: %s, req tenant: %d, chunk tenant: %d",
			chunkID, tenantID.(uint64), chunk.TenantID,
		)
		c.Error(errors.NewForbiddenError("No permission to access this chunk"))
		return
	}

	// 청크 내용에 대한 보안 정리
	if chunk.Content != "" {
		chunk.Content = secutils.SanitizeForDisplay(chunk.Content)
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    chunk,
	})
}

// ListKnowledgeChunks godoc
// @Summary      지식 청크 목록 조회
// @Description  지정된 지식 하의 모든 청크 목록 조회, 페이징 지원
// @Tags         청크 관리
// @Accept       json
// @Produce      json
// @Param        knowledge_id  path      string  true   "지식 ID"
// @Param        page          query     int     false  "페이지 번호"  default(1)
// @Param        page_size     query     int     false  "페이지당 항목 수"  default(10)
// @Success      200           {object}  map[string]interface{}  "청크 목록"
// @Failure      400           {object}  errors.AppError         "요청 매개변수 오류"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /chunks/{knowledge_id} [get]
func (h *ChunkHandler) ListKnowledgeChunks(c *gin.Context) {
	ctx := c.Request.Context()
	logger.Info(ctx, "Start retrieving knowledge chunks list")

	knowledgeID := secutils.SanitizeForLog(c.Param("knowledge_id"))
	if knowledgeID == "" {
		logger.Error(ctx, "Knowledge ID is empty")
		c.Error(errors.NewBadRequestError("Knowledge ID cannot be empty"))
		return
	}

	// 페이징 매개변수 파싱
	var pagination types.Pagination
	if err := c.ShouldBindQuery(&pagination); err != nil {
		logger.Errorf(ctx, "Failed to parse pagination parameters: %s", secutils.SanitizeForLog(err.Error()))
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	if pagination.Page < 1 {
		pagination.Page = 1
	}
	if pagination.PageSize < 1 {
		pagination.PageSize = 10
	}
	if pagination.PageSize > 100 {
		pagination.PageSize = 100
	}

	chunkType := []types.ChunkType{types.ChunkTypeText}

	// 쿼리에 페이징 사용
	result, err := h.service.ListPagedChunksByKnowledgeID(ctx, knowledgeID, &pagination, chunkType)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	// 청크 내용에 대한 보안 정리
	for _, chunk := range result.Data.([]*types.Chunk) {
		if chunk.Content != "" {
			chunk.Content = secutils.SanitizeForDisplay(chunk.Content)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success":   true,
		"data":      result.Data,
		"total":     result.Total,
		"page":      result.Page,
		"page_size": result.PageSize,
	})
}

// UpdateChunkRequest 청크 업데이트를 위한 요청 구조 정의
type UpdateChunkRequest struct {
	Content    string    `json:"content"`
	Embedding  []float32 `json:"embedding"`
	ChunkIndex int       `json:"chunk_index"`
	IsEnabled  bool      `json:"is_enabled"`
	StartAt    int       `json:"start_at"`
	EndAt      int       `json:"end_at"`
	ImageInfo  string    `json:"image_info"`
}

// validateAndGetChunk 요청 매개변수 검증 및 청크 조회
// 청크 정보, 지식 ID, 오류 반환
func (h *ChunkHandler) validateAndGetChunk(c *gin.Context) (*types.Chunk, string, error) {
	ctx := c.Request.Context()

	// 지식 ID 검증
	knowledgeID := secutils.SanitizeForLog(c.Param("knowledge_id"))
	if knowledgeID == "" {
		logger.Error(ctx, "Knowledge ID is empty")
		return nil, "", errors.NewBadRequestError("Knowledge ID cannot be empty")
	}

	// 청크 ID 검증
	id := secutils.SanitizeForLog(c.Param("id"))
	if id == "" {
		logger.Error(ctx, "Chunk ID is empty")
		return nil, knowledgeID, errors.NewBadRequestError("Chunk ID cannot be empty")
	}

	// 컨텍스트에서 테넌트 ID 가져오기
	tenantID, exists := c.Get(types.TenantIDContextKey.String())
	if !exists {
		logger.Error(ctx, "Failed to get tenant ID")
		return nil, knowledgeID, errors.NewUnauthorizedError("Unauthorized")
	}

	logger.Infof(ctx, "Retrieving knowledge chunk information, knowledge ID: %s, chunk ID: %s", knowledgeID, id)

	// 기존 청크 가져오기
	chunk, err := h.service.GetChunkByID(ctx, id)
	if err != nil {
		if err == service.ErrChunkNotFound {
			logger.Warnf(ctx, "Chunk not found, knowledge ID: %s, chunk ID: %s", knowledgeID, id)
			return nil, knowledgeID, errors.NewNotFoundError("Chunk not found")
		}
		logger.ErrorWithFields(ctx, err, nil)
		return nil, knowledgeID, errors.NewInternalServerError(err.Error())
	}

	// 테넌트 ID 검증
	if chunk.TenantID != tenantID.(uint64) || chunk.KnowledgeID != knowledgeID {
		logger.Warnf(
			ctx,
			"Tenant has no permission to access chunk, knowledge ID: %s, chunk ID: %s, req tenant: %d, chunk tenant: %d",
			knowledgeID,
			id,
			tenantID,
			chunk.TenantID,
		)
		return nil, knowledgeID, errors.NewForbiddenError("No permission to access this chunk")
	}

	return chunk, knowledgeID, nil
}

// UpdateChunk godoc
// @Summary      청크 업데이트
// @Description  지정된 청크의 내용 및 속성 업데이트
// @Tags         청크 관리
// @Accept       json
// @Produce      json
// @Param        knowledge_id  path      string              true  "지식 ID"
// @Param        id            path      string              true  "청크 ID"
// @Param        request       body      UpdateChunkRequest  true  "업데이트 요청"
// @Success      200           {object}  map[string]interface{}  "업데이트된 청크"
// @Failure      400           {object}  errors.AppError         "요청 매개변수 오류"
// @Failure      404           {object}  errors.AppError         "청크를 찾을 수 없음"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /chunks/{knowledge_id}/{id} [put]
func (h *ChunkHandler) UpdateChunk(c *gin.Context) {
	ctx := c.Request.Context()
	logger.Info(ctx, "Start updating knowledge chunk")

	// 매개변수 검증 및 청크 가져오기
	chunk, knowledgeID, err := h.validateAndGetChunk(c)
	if err != nil {
		c.Error(err)
		return
	}
	var req UpdateChunkRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Errorf(ctx, "Failed to parse request parameters: %s", secutils.SanitizeForLog(err.Error()))
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	// 청크 속성 업데이트
	if req.Content != "" {
		chunk.Content = req.Content
	}

	chunk.IsEnabled = req.IsEnabled

	if err := h.service.UpdateChunk(ctx, chunk); err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(ctx, "Knowledge chunk updated successfully, knowledge ID: %s, chunk ID: %s",
		secutils.SanitizeForLog(knowledgeID), secutils.SanitizeForLog(chunk.ID))
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    chunk,
	})
}

// DeleteChunk godoc
// @Summary      청크 삭제
// @Description  지정된 청크 삭제
// @Tags         청크 관리
// @Accept       json
// @Produce      json
// @Param        knowledge_id  path      string  true  "지식 ID"
// @Param        id            path      string  true  "청크 ID"
// @Success      200           {object}  map[string]interface{}  "삭제 성공"
// @Failure      400           {object}  errors.AppError         "요청 매개변수 오류"
// @Failure      404           {object}  errors.AppError         "청크를 찾을 수 없음"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /chunks/{knowledge_id}/{id} [delete]
func (h *ChunkHandler) DeleteChunk(c *gin.Context) {
	ctx := c.Request.Context()
	logger.Info(ctx, "Start deleting knowledge chunk")

	// 매개변수 검증 및 청크 가져오기
	chunk, _, err := h.validateAndGetChunk(c)
	if err != nil {
		c.Error(err)
		return
	}

	if err := h.service.DeleteChunk(ctx, chunk.ID); err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Chunk deleted",
	})
}

// DeleteChunksByKnowledgeID godoc
// @Summary      지식 하의 모든 청크 삭제
// @Description  지정된 지식 하의 모든 청크 삭제
// @Tags         청크 관리
// @Accept       json
// @Produce      json
// @Param        knowledge_id  path      string  true  "지식 ID"
// @Success      200           {object}  map[string]interface{}  "삭제 성공"
// @Failure      400           {object}  errors.AppError         "요청 매개변수 오류"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /chunks/{knowledge_id} [delete]
func (h *ChunkHandler) DeleteChunksByKnowledgeID(c *gin.Context) {
	ctx := c.Request.Context()
	logger.Info(ctx, "Start deleting all chunks under knowledge")

	knowledgeID := secutils.SanitizeForLog(c.Param("knowledge_id"))
	if knowledgeID == "" {
		logger.Error(ctx, "Knowledge ID is empty")
		c.Error(errors.NewBadRequestError("Knowledge ID cannot be empty"))
		return
	}

	// 지식 하의 모든 청크 삭제
	err := h.service.DeleteChunksByKnowledgeID(ctx, knowledgeID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "All chunks under knowledge deleted",
	})
}

// DeleteGeneratedQuestion godoc
// @Summary      생성된 질문 삭제
// @Description  청크에서 생성된 질문 삭제
// @Tags         청크 관리
// @Accept       json
// @Produce      json
// @Param        id       path      string                       true  "청크 ID"
// @Param        request  body      object{question_id=string}   true  "질문 ID"
// @Success      200      {object}  map[string]interface{}       "삭제 성공"
// @Failure      400      {object}  errors.AppError              "요청 매개변수 오류"
// @Failure      404      {object}  errors.AppError              "청크를 찾을 수 없음"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /chunks/by-id/{id}/questions [delete]
func (h *ChunkHandler) DeleteGeneratedQuestion(c *gin.Context) {
	ctx := c.Request.Context()
	logger.Info(ctx, "Start deleting generated question from chunk")

	chunkID := secutils.SanitizeForLog(c.Param("id"))
	if chunkID == "" {
		logger.Error(ctx, "Chunk ID is empty")
		c.Error(errors.NewBadRequestError("Chunk ID cannot be empty"))
		return
	}

	var req struct {
		QuestionID string `json:"question_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Errorf(ctx, "Failed to parse request parameters: %s", secutils.SanitizeForLog(err.Error()))
		c.Error(errors.NewBadRequestError("Question ID is required"))
		return
	}

	// 컨텍스트에서 테넌트 ID 가져오기
	tenantID, exists := c.Get(types.TenantIDContextKey.String())
	if !exists {
		logger.Error(ctx, "Failed to get tenant ID")
		c.Error(errors.NewUnauthorizedError("Unauthorized"))
		return
	}

	// 청크가 존재하고 테넌트에 속하는지 확인
	chunk, err := h.service.GetChunkByID(ctx, chunkID)
	if err != nil {
		if err == service.ErrChunkNotFound {
			logger.Warnf(ctx, "Chunk not found, chunk ID: %s", chunkID)
			c.Error(errors.NewNotFoundError("Chunk not found"))
			return
		}
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	if chunk.TenantID != tenantID.(uint64) {
		logger.Warnf(ctx, "Tenant has no permission to access chunk, chunk ID: %s", chunkID)
		c.Error(errors.NewForbiddenError("No permission to access this chunk"))
		return
	}

	// ID로 생성된 질문 삭제
	if err := h.service.DeleteGeneratedQuestion(ctx, chunkID, req.QuestionID); err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	logger.Infof(ctx, "Generated question deleted successfully, chunk ID: %s, question ID: %s",
		secutils.SanitizeForLog(chunkID), secutils.SanitizeForLog(req.QuestionID))
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Generated question deleted",
	})
}
