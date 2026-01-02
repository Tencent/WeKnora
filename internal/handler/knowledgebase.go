package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

// KnowledgeBaseHandler 지식베이스 작업을 위한 HTTP 핸들러 정의
type KnowledgeBaseHandler struct {
	service          interfaces.KnowledgeBaseService
	knowledgeService interfaces.KnowledgeService
	asynqClient      *asynq.Client
}

// NewKnowledgeBaseHandler 새로운 지식베이스 핸들러 인스턴스 생성
func NewKnowledgeBaseHandler(
	service interfaces.KnowledgeBaseService,
	knowledgeService interfaces.KnowledgeService,
	asynqClient *asynq.Client,
) *KnowledgeBaseHandler {
	return &KnowledgeBaseHandler{
		service:          service,
		knowledgeService: knowledgeService,
		asynqClient:      asynqClient,
	}
}

// HybridSearch godoc
// @Summary      하이브리드 검색
// @Description  지식베이스에서 벡터 및 키워드 하이브리드 검색 수행
// @Tags         지식베이스
// @Accept       json
// @Produce      json
// @Param        id       path      string             true  "지식베이스 ID"
// @Param        request  body      types.SearchParams true  "검색 매개변수"
// @Success      200      {object}  map[string]interface{}  "검색 결과"
// @Failure      400      {object}  errors.AppError         "요청 매개변수 오류"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/hybrid-search [get]
func (h *KnowledgeBaseHandler) HybridSearch(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start hybrid search")

	// 지식베이스 ID 검증
	id := secutils.SanitizeForLog(c.Param("id"))
	if id == "" {
		logger.Error(ctx, "Knowledge base ID is empty")
		c.Error(errors.NewBadRequestError("Knowledge base ID cannot be empty"))
		return
	}

	// 요청 본문 파싱
	var req types.SearchParams
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		c.Error(errors.NewBadRequestError("Invalid request parameters").WithDetails(err.Error()))
		return
	}

	logger.Infof(ctx, "Executing hybrid search, knowledge base ID: %s, query: %s",
		secutils.SanitizeForLog(id), secutils.SanitizeForLog(req.QueryText))

	// 기본 검색 매개변수로 하이브리드 검색 실행
	results, err := h.service.HybridSearch(ctx, id, req)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(ctx, "Hybrid search completed, knowledge base ID: %s, result count: %d",
		secutils.SanitizeForLog(id), len(results))
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    results,
	})
}

// CreateKnowledgeBase godoc
// @Summary      지식베이스 생성
// @Description  새로운 지식베이스 생성
// @Tags         지식베이스
// @Accept       json
// @Produce      json
// @Param        request  body      types.KnowledgeBase  true  "지식베이스 정보"
// @Success      201      {object}  map[string]interface{}  "생성된 지식베이스"
// @Failure      400      {object}  errors.AppError         "요청 매개변수 오류"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases [post]
func (h *KnowledgeBaseHandler) CreateKnowledgeBase(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start creating knowledge base")

	// 요청 본문 파싱
	var req types.KnowledgeBase
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		c.Error(errors.NewBadRequestError("Invalid request parameters").WithDetails(err.Error()))
		return
	}
	if err := validateExtractConfig(req.ExtractConfig); err != nil {
		logger.Error(ctx, "Invalid extract configuration", err)
		c.Error(err)
		return
	}

	logger.Infof(ctx, "Creating knowledge base, name: %s", secutils.SanitizeForLog(req.Name))
	// 서비스를 사용하여 지식베이스 생성
	kb, err := h.service.CreateKnowledgeBase(ctx, &req)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(ctx, "Knowledge base created successfully, ID: %s, name: %s",
		secutils.SanitizeForLog(kb.ID), secutils.SanitizeForLog(kb.Name))
	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data":    kb,
	})
}

// validateAndGetKnowledgeBase 요청 매개변수 검증 및 지식베이스 조회
// 지식베이스, 지식베이스 ID 및 발생한 오류 반환
func (h *KnowledgeBaseHandler) validateAndGetKnowledgeBase(c *gin.Context) (*types.KnowledgeBase, string, error) {
	ctx := c.Request.Context()

	// 컨텍스트에서 테넌트 ID 가져오기
	tenantID, exists := c.Get(types.TenantIDContextKey.String())
	if !exists {
		logger.Error(ctx, "Failed to get tenant ID")
		return nil, "", errors.NewUnauthorizedError("Unauthorized")
	}

	// URL 매개변수에서 지식베이스 ID 가져오기
	id := secutils.SanitizeForLog(c.Param("id"))
	if id == "" {
		logger.Error(ctx, "Knowledge base ID is empty")
		return nil, "", errors.NewBadRequestError("Knowledge base ID cannot be empty")
	}

	// 테넌트가 이 지식베이스에 액세스할 수 있는 권한이 있는지 확인
	kb, err := h.service.GetKnowledgeBaseByID(ctx, id)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		return nil, id, errors.NewInternalServerError(err.Error())
	}

	// 테넌트 소유권 확인
	if kb.TenantID != tenantID.(uint64) {
		logger.Warnf(
			ctx,
			"Tenant has no permission to access this knowledge base, knowledge base ID: %s, "+
				"request tenant ID: %d, knowledge base tenant ID: %d",
			id, tenantID.(uint64), kb.TenantID,
		)
		return nil, id, errors.NewForbiddenError("No permission to operate")
	}

	return kb, id, nil
}

// GetKnowledgeBase godoc
// @Summary      지식베이스 상세 정보 조회
// @Description  ID를 기반으로 지식베이스 상세 정보 조회
// @Tags         지식베이스
// @Accept       json
// @Produce      json
// @Param        id   path      string  true  "지식베이스 ID"
// @Success      200  {object}  map[string]interface{}  "지식베이스 상세 정보"
// @Failure      400  {object}  errors.AppError         "요청 매개변수 오류"
// @Failure      404  {object}  errors.AppError         "지식베이스를 찾을 수 없음"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id} [get]
func (h *KnowledgeBaseHandler) GetKnowledgeBase(c *gin.Context) {
	// 지식베이스 검증 및 가져오기
	kb, _, err := h.validateAndGetKnowledgeBase(c)
	if err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    kb,
	})
}

// ListKnowledgeBases godoc
// @Summary      지식베이스 목록 조회
// @Description  현재 테넌트의 모든 지식베이스 조회
// @Tags         지식베이스
// @Accept       json
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "지식베이스 목록"
// @Failure      500  {object}  errors.AppError         "서버 오류"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases [get]
func (h *KnowledgeBaseHandler) ListKnowledgeBases(c *gin.Context) {
	ctx := c.Request.Context()

	// 이 테넌트의 모든 지식베이스 가져오기
	kbs, err := h.service.ListKnowledgeBases(ctx)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    kbs,
	})
}

// UpdateKnowledgeBaseRequest 지식베이스 업데이트를 위한 요청 본문 구조 정의
type UpdateKnowledgeBaseRequest struct {
	Name        string                     `json:"name"        binding:"required"`
	Description string                     `json:"description"`
	Config      *types.KnowledgeBaseConfig `json:"config"      binding:"required"`
}

// UpdateKnowledgeBase godoc
// @Summary      지식베이스 업데이트
// @Description  지식베이스의 이름, 설명 및 구성 업데이트
// @Tags         지식베이스
// @Accept       json
// @Produce      json
// @Param        id       path      string                     true  "지식베이스 ID"
// @Param        request  body      UpdateKnowledgeBaseRequest true  "업데이트 요청"
// @Success      200      {object}  map[string]interface{}     "업데이트된 지식베이스"
// @Failure      400      {object}  errors.AppError            "요청 매개변수 오류"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id} [put]
func (h *KnowledgeBaseHandler) UpdateKnowledgeBase(c *gin.Context) {
	ctx := c.Request.Context()
	logger.Info(ctx, "Start updating knowledge base")

	// 지식베이스 검증 및 가져오기
	_, id, err := h.validateAndGetKnowledgeBase(c)
	if err != nil {
		c.Error(err)
		return
	}

	// 요청 본문 파싱
	var req UpdateKnowledgeBaseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		c.Error(errors.NewBadRequestError("Invalid request parameters").WithDetails(err.Error()))
		return
	}

	logger.Infof(ctx, "Updating knowledge base, ID: %s, name: %s",
		secutils.SanitizeForLog(id), secutils.SanitizeForLog(req.Name))

	// 지식베이스 업데이트
	kb, err := h.service.UpdateKnowledgeBase(ctx, id, req.Name, req.Description, req.Config)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(ctx, "Knowledge base updated successfully, ID: %s",
		secutils.SanitizeForLog(id))
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    kb,
	})
}

// DeleteKnowledgeBase godoc
// @Summary      지식베이스 삭제
// @Description  지정된 지식베이스 및 모든 내용 삭제
// @Tags         지식베이스
// @Accept       json
// @Produce      json
// @Param        id   path      string  true  "지식베이스 ID"
// @Success      200  {object}  map[string]interface{}  "삭제 성공"
// @Failure      400  {object}  errors.AppError         "요청 매개변수 오류"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id} [delete]
func (h *KnowledgeBaseHandler) DeleteKnowledgeBase(c *gin.Context) {
	ctx := c.Request.Context()
	logger.Info(ctx, "Start deleting knowledge base")

	// 지식베이스 검증 및 가져오기
	kb, id, err := h.validateAndGetKnowledgeBase(c)
	if err != nil {
		c.Error(err)
		return
	}

	logger.Infof(ctx, "Deleting knowledge base, ID: %s, name: %s",
		secutils.SanitizeForLog(id), secutils.SanitizeForLog(kb.Name))

	// 지식베이스 삭제
	if err := h.service.DeleteKnowledgeBase(ctx, id); err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(ctx, "Knowledge base deleted successfully, ID: %s",
		secutils.SanitizeForLog(id))
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Knowledge base deleted successfully",
	})
}

type CopyKnowledgeBaseRequest struct {
	SourceID string `json:"source_id" binding:"required"`
	TargetID string `json:"target_id"`
}

// CopyKnowledgeBaseResponse 지식베이스 복사 응답 정의
type CopyKnowledgeBaseResponse struct {
	TaskID   string `json:"task_id"`
	SourceID string `json:"source_id"`
	TargetID string `json:"target_id"`
	Message  string `json:"message"`
}

// CopyKnowledgeBase godoc
// @Summary      지식베이스 복사
// @Description  지식베이스의 내용을 다른 지식베이스로 복사 (비동기 작업)
// @Tags         지식베이스
// @Accept       json
// @Produce      json
// @Param        request  body      CopyKnowledgeBaseRequest   true  "복사 요청"
// @Success      200      {object}  map[string]interface{}     "작업 ID"
// @Failure      400      {object}  errors.AppError            "요청 매개변수 오류"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/copy [post]
func (h *KnowledgeBaseHandler) CopyKnowledgeBase(c *gin.Context) {
	ctx := c.Request.Context()
	var req CopyKnowledgeBaseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		c.Error(errors.NewBadRequestError("Invalid request parameters").WithDetails(err.Error()))
		return
	}

	// 컨텍스트에서 테넌트 ID 가져오기
	tenantID, exists := c.Get(types.TenantIDContextKey.String())
	if !exists {
		logger.Error(ctx, "Failed to get tenant ID")
		c.Error(errors.NewUnauthorizedError("Unauthorized"))
		return
	}

	// 작업 ID 생성
	taskID := uuid.New().String()

	// KB 복제 페이로드 생성
	payload := types.KBClonePayload{
		TenantID: tenantID.(uint64),
		TaskID:   taskID,
		SourceID: req.SourceID,
		TargetID: req.TargetID,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		logger.Errorf(ctx, "Failed to marshal KB clone payload: %v", err)
		c.Error(errors.NewInternalServerError("Failed to create task"))
		return
	}

	// KB 복제 작업을 Asynq에 등록
	task := asynq.NewTask(types.TypeKBClone, payloadBytes, asynq.Queue("default"), asynq.MaxRetry(3))
	info, err := h.asynqClient.Enqueue(task)
	if err != nil {
		logger.Errorf(ctx, "Failed to enqueue KB clone task: %v", err)
		c.Error(errors.NewInternalServerError("Failed to enqueue task"))
		return
	}

	logger.Infof(ctx, "KB clone task enqueued: %s, asynq task ID: %s, source: %s, target: %s",
		taskID, info.ID, secutils.SanitizeForLog(req.SourceID), secutils.SanitizeForLog(req.TargetID))

	// 프론트엔드에서 즉시 조회할 수 있도록 Redis에 초기 진행 상황 저장
	initialProgress := &types.KBCloneProgress{
		TaskID:    taskID,
		SourceID:  req.SourceID,
		TargetID:  req.TargetID,
		Status:    types.KBCloneStatusPending,
		Progress:  0,
		Message:   "Task queued, waiting to start...",
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
	}
	if err := h.knowledgeService.SaveKBCloneProgress(ctx, initialProgress); err != nil {
		logger.Warnf(ctx, "Failed to save initial KB clone progress: %v", err)
		// 요청을 실패 처리하지 않음, 작업은 이미 대기열에 추가됨
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": CopyKnowledgeBaseResponse{
			TaskID:   taskID,
			SourceID: req.SourceID,
			TargetID: req.TargetID,
			Message:  "Knowledge base copy task started",
		},
	})
}

// GetKBCloneProgress godoc
// @Summary      지식베이스 복사 진행 상황 조회
// @Description  지식베이스 복사 작업의 진행 상황 조회
// @Tags         지식베이스
// @Accept       json
// @Produce      json
// @Param        task_id  path      string  true  "작업 ID"
// @Success      200      {object}  map[string]interface{}  "진행 정보"
// @Failure      404      {object}  errors.AppError         "작업을 찾을 수 없음"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/copy/progress/{task_id} [get]
func (h *KnowledgeBaseHandler) GetKBCloneProgress(c *gin.Context) {
	ctx := c.Request.Context()

	taskID := c.Param("task_id")
	if taskID == "" {
		logger.Error(ctx, "Task ID is empty")
		c.Error(errors.NewBadRequestError("Task ID cannot be empty"))
		return
	}

	progress, err := h.knowledgeService.GetKBCloneProgress(ctx, taskID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    progress,
	})
}

// validateExtractConfig 그래프 구성 매개변수 검증
func validateExtractConfig(config *types.ExtractConfig) error {
	logger.Errorf(context.Background(), "Validating extract configuration: %+v", config)
	if config == nil {
		return nil
	}
	if !config.Enabled {
		*config = types.ExtractConfig{Enabled: false}
		return nil
	}
	// 텍스트 필드 검증
	if config.Text == "" {
		return errors.NewBadRequestError("text cannot be empty")
	}

	// 태그 필드 검증
	if len(config.Tags) == 0 {
		return errors.NewBadRequestError("tags cannot be empty")
	}
	for i, tag := range config.Tags {
		if tag == "" {
			return errors.NewBadRequestError("tag cannot be empty at index " + strconv.Itoa(i))
		}
	}

	// 노드 검증
	if len(config.Nodes) == 0 {
		return errors.NewBadRequestError("nodes cannot be empty")
	}
	nodeNames := make(map[string]bool)
	for i, node := range config.Nodes {
		if node.Name == "" {
			return errors.NewBadRequestError("node name cannot be empty at index " + strconv.Itoa(i))
		}
		// 중복 노드 이름 확인
		if nodeNames[node.Name] {
			return errors.NewBadRequestError("duplicate node name: " + node.Name)
		}
		nodeNames[node.Name] = true
	}

	if len(config.Relations) == 0 {
		return errors.NewBadRequestError("relations cannot be empty")
	}
	// 관계 검증
	for i, relation := range config.Relations {
		if relation.Node1 == "" {
			return errors.NewBadRequestError("relation node1 cannot be empty at index " + strconv.Itoa(i))
		}
		if relation.Node2 == "" {
			return errors.NewBadRequestError("relation node2 cannot be empty at index " + strconv.Itoa(i))
		}
		if relation.Type == "" {
			return errors.NewBadRequestError("relation type cannot be empty at index " + strconv.Itoa(i))
		}
		// 참조된 노드가 존재하는지 확인
		if !nodeNames[relation.Node1] {
			return errors.NewBadRequestError("relation references non-existent node1: " + relation.Node1)
		}
		if !nodeNames[relation.Node2] {
			return errors.NewBadRequestError("relation references non-existent node2: " + relation.Node2)
		}
	}

	return nil
}
