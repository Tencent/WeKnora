package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
)

// KnowledgeHandler 지식 리소스 관련 HTTP 요청 처리
type KnowledgeHandler struct {
	kgService interfaces.KnowledgeService
	kbService interfaces.KnowledgeBaseService
}

// NewKnowledgeHandler 새로운 KnowledgeHandler 인스턴스 생성
func NewKnowledgeHandler(
	kgService interfaces.KnowledgeService,
	kbService interfaces.KnowledgeBaseService,
) *KnowledgeHandler {
	return &KnowledgeHandler{kgService: kgService, kbService: kbService}
}

// validateKnowledgeBaseAccess 지식베이스에 대한 접근 권한을 확인합니다.
// 지식베이스 객체, 지식베이스 ID, 그리고 오류 발생 시 오류를 반환합니다.
func (h *KnowledgeHandler) validateKnowledgeBaseAccess(c *gin.Context) (*types.KnowledgeBase, string, error) {
	ctx := c.Request.Context()

	// URL 경로 매개변수에서 지식베이스 ID 가져오기
	kbID := secutils.SanitizeForLog(c.Param("id"))
	if kbID == "" {
		logger.Error(ctx, "Knowledge base ID is empty")
		return nil, "", errors.NewBadRequestError("Knowledge base ID cannot be empty")
	}

	// 지식베이스 상세 정보 가져오기
	kb, err := h.kbService.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		return nil, kbID, errors.NewInternalServerError(err.Error())
	}

	// 테넌트 권한 확인
	if kb.TenantID != c.GetUint64(types.TenantIDContextKey.String()) {
		logger.Warnf(
			ctx,
			"Permission denied to access this knowledge base, tenant ID mismatch, "+
				"requested tenant ID: %d, knowledge base tenant ID: %d",
			c.GetUint64(types.TenantIDContextKey.String()),
			kb.TenantID,
		)
		return nil, kbID, errors.NewForbiddenError("Permission denied to access this knowledge base")
	}

	return kb, kbID, nil
}

// handleDuplicateKnowledgeError 중복된 지식이 감지된 경우를 처리합니다.
// 중복 오류가 감지되어 처리된 경우 true를 반환하고, 그렇지 않으면 false를 반환합니다.
func (h *KnowledgeHandler) handleDuplicateKnowledgeError(c *gin.Context,
	err error, knowledge *types.Knowledge, duplicateType string,
) bool {
	if dupErr, ok := err.(*types.DuplicateKnowledgeError); ok {
		ctx := c.Request.Context()
		logger.Warnf(ctx, "Detected duplicate %s: %s", duplicateType, secutils.SanitizeForLog(dupErr.Error()))
		c.JSON(http.StatusConflict, gin.H{
			"success": false,
			"message": dupErr.Error(),
			"data":    knowledge, // knowledge에는 기존 문서 정보가 포함됨
			"code":    fmt.Sprintf("duplicate_%s", duplicateType),
		})
		return true
	}
	return false
}

// CreateKnowledgeFromFile godoc
// @Summary      파일에서 지식 생성
// @Description  파일을 업로드하고 지식 항목 생성
// @Tags         지식 관리
// @Accept       multipart/form-data
// @Produce      json
// @Param        id                path      string  true   "지식베이스 ID"
// @Param        file              formData  file    true   "업로드할 파일"
// @Param        fileName          formData  string  false  "사용자 지정 파일명"
// @Param        metadata          formData  string  false  "메타데이터 JSON"
// @Param        enable_multimodel formData  bool    false  "멀티모달 처리 활성화"
// @Success      200               {object}  map[string]interface{}  "생성된 지식"
// @Failure      400               {object}  errors.AppError         "요청 매개변수 오류"
// @Failure      409               {object}  map[string]interface{}  "파일 중복"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/knowledge/file [post]
func (h *KnowledgeHandler) CreateKnowledgeFromFile(c *gin.Context) {
	ctx := c.Request.Context()
	logger.Info(ctx, "Start creating knowledge from file")

	// 지식베이스 접근 권한 확인
	_, kbID, err := h.validateKnowledgeBaseAccess(c)
	if err != nil {
		c.Error(err)
		return
	}

	// 업로드된 파일 가져오기
	file, err := c.FormFile("file")
	if err != nil {
		logger.Error(ctx, "File upload failed", err)
		c.Error(errors.NewBadRequestError("File upload failed").WithDetails(err.Error()))
		return
	}

	// 파일 크기 확인 (MAX_FILE_SIZE_MB를 통해 구성 가능)
	maxSize := secutils.GetMaxFileSize()
	if file.Size > maxSize {
		logger.Error(ctx, "File size too large")
		c.Error(errors.NewBadRequestError(fmt.Sprintf("파일 크기는 %dMB를 초과할 수 없습니다", secutils.GetMaxFileSizeMB())))
		return
	}

	// 사용자 지정 파일명 가져오기 (폴더 업로드 시 경로 포함)
	customFileName := c.PostForm("fileName")
	customFileName = secutils.SanitizeForLog(customFileName)
	displayFileName := file.Filename
	displayFileName = secutils.SanitizeForLog(displayFileName)
	if customFileName != "" {
		displayFileName = customFileName
		logger.Infof(ctx, "Using custom filename: %s (original: %s)", customFileName, displayFileName)
	}

	logger.Infof(ctx, "File upload successful, filename: %s, size: %.2f KB", displayFileName, float64(file.Size)/1024)
	logger.Infof(ctx, "Creating knowledge, knowledge base ID: %s, filename: %s", kbID, displayFileName)

	// 메타데이터 파싱 (제공된 경우)
	var metadata map[string]string
	metadataStr := c.PostForm("metadata")
	if metadataStr != "" {
		if err := json.Unmarshal([]byte(metadataStr), &metadata); err != nil {
			logger.Error(ctx, "Failed to parse metadata", err)
			c.Error(errors.NewBadRequestError("Invalid metadata format").WithDetails(err.Error()))
			return
		}
		logger.Infof(ctx, "Received file metadata: %s", secutils.SanitizeForLog(fmt.Sprintf("%v", metadata)))
	}

	enableMultimodelForm := c.PostForm("enable_multimodel")
	var enableMultimodel *bool
	if enableMultimodelForm != "" {
		parseBool, err := strconv.ParseBool(enableMultimodelForm)
		if err != nil {
			logger.Error(ctx, "Failed to parse enable_multimodel", err)
			c.Error(errors.NewBadRequestError("Invalid enable_multimodel format").WithDetails(err.Error()))
			return
		}
		enableMultimodel = &parseBool
	}

	// 파일에서 지식 항목 생성
	knowledge, err := h.kgService.CreateKnowledgeFromFile(ctx, kbID, file, metadata, enableMultimodel, customFileName)
	// 중복 지식 오류 확인
	if err != nil {
		if h.handleDuplicateKnowledgeError(c, err, knowledge, "file") {
			return
		}
		if appErr, ok := errors.IsAppError(err); ok {
			c.Error(appErr)
			return
		}
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(
		ctx,
		"Knowledge created successfully, ID: %s, title: %s",
		secutils.SanitizeForLog(knowledge.ID),
		secutils.SanitizeForLog(knowledge.Title),
	)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    knowledge,
	})
}

// CreateKnowledgeFromURL godoc
// @Summary      URL에서 지식 생성
// @Description  지정된 URL에서 내용을 가져와 지식 항목 생성
// @Tags         지식 관리
// @Accept       json
// @Produce      json
// @Param        id       path      string  true  "지식베이스 ID"
// @Param        request  body      object{url=string,enable_multimodel=bool,title=string}  true  "URL 요청"
// @Success      201      {object}  map[string]interface{}  "생성된 지식"
// @Failure      400      {object}  errors.AppError         "요청 매개변수 오류"
// @Failure      409      {object}  map[string]interface{}  "URL 중복"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/knowledge/url [post]
func (h *KnowledgeHandler) CreateKnowledgeFromURL(c *gin.Context) {
	ctx := c.Request.Context()
	logger.Info(ctx, "Start creating knowledge from URL")

	// 지식베이스 접근 권한 확인
	_, kbID, err := h.validateKnowledgeBaseAccess(c)
	if err != nil {
		c.Error(err)
		return
	}

	// 요청 본문에서 URL 파싱
	var req struct {
		URL              string `json:"url" binding:"required"`
		EnableMultimodel *bool  `json:"enable_multimodel"`
		Title            string `json:"title"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse URL request", err)
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	logger.Infof(ctx, "Received URL request: %s", secutils.SanitizeForLog(req.URL))
	logger.Infof(
		ctx,
		"Creating knowledge from URL, knowledge base ID: %s, URL: %s",
		secutils.SanitizeForLog(kbID),
		secutils.SanitizeForLog(req.URL),
	)

	// URL에서 지식 항목 생성
	knowledge, err := h.kgService.CreateKnowledgeFromURL(ctx, kbID, req.URL, req.EnableMultimodel, req.Title)
	// 중복 지식 오류 확인
	if err != nil {
		if h.handleDuplicateKnowledgeError(c, err, knowledge, "url") {
			return
		}
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(
		ctx,
		"Knowledge created successfully from URL, ID: %s, title: %s",
		secutils.SanitizeForLog(knowledge.ID),
		secutils.SanitizeForLog(knowledge.Title),
	)
	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data":    knowledge,
	})
}

// CreateManualKnowledge godoc
// @Summary      수동 지식 생성
// @Description  Markdown 형식의 지식 내용을 수동으로 입력하여 생성
// @Tags         지식 관리
// @Accept       json
// @Produce      json
// @Param        id       path      string                       true  "지식베이스 ID"
// @Param        request  body      types.ManualKnowledgePayload true  "수동 지식 내용"
// @Success      200      {object}  map[string]interface{}       "생성된 지식"
// @Failure      400      {object}  errors.AppError              "요청 매개변수 오류"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/knowledge/manual [post]
func (h *KnowledgeHandler) CreateManualKnowledge(c *gin.Context) {
	ctx := c.Request.Context()
	logger.Info(ctx, "Start creating manual knowledge")

	_, kbID, err := h.validateKnowledgeBaseAccess(c)
	if err != nil {
		c.Error(err)
		return
	}

	var req types.ManualKnowledgePayload
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse manual knowledge request", err)
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	knowledge, err := h.kgService.CreateKnowledgeFromManual(ctx, kbID, &req)
	if err != nil {
		if appErr, ok := errors.IsAppError(err); ok {
			c.Error(appErr)
			return
		}
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"kb_id": kbID,
		})
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(ctx, "Manual knowledge created successfully, knowledge ID: %s",
		secutils.SanitizeForLog(knowledge.ID))
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    knowledge,
	})
}

// GetKnowledge godoc
// @Summary      지식 상세 조회
// @Description  ID로 지식 항목 상세 정보 조회
// @Tags         지식 관리
// @Accept       json
// @Produce      json
// @Param        id   path      string  true  "지식 ID"
// @Success      200  {object}  map[string]interface{}  "지식 상세"
// @Failure      400  {object}  errors.AppError         "요청 매개변수 오류"
// @Failure      404  {object}  errors.AppError         "지식을 찾을 수 없음"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge/{id} [get]
func (h *KnowledgeHandler) GetKnowledge(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start retrieving knowledge")

	// URL 경로 매개변수에서 지식 ID 가져오기
	id := secutils.SanitizeForLog(c.Param("id"))
	if id == "" {
		logger.Error(ctx, "Knowledge ID is empty")
		c.Error(errors.NewBadRequestError("Knowledge ID cannot be empty"))
		return
	}

	logger.Infof(ctx, "Retrieving knowledge, ID: %s", secutils.SanitizeForLog(id))
	knowledge, err := h.kgService.GetKnowledgeByID(ctx, id)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(
		ctx,
		"Knowledge retrieved successfully, ID: %s, title: %s",
		secutils.SanitizeForLog(knowledge.ID),
		secutils.SanitizeForLog(knowledge.Title),
	)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    knowledge,
	})
}

// ListKnowledge godoc
// @Summary      지식 목록 조회
// @Description  지식베이스 내의 지식 목록 조회, 페이징 및 필터링 지원
// @Tags         지식 관리
// @Accept       json
// @Produce      json
// @Param        id         path      string  true   "지식베이스 ID"
// @Param        page       query     int     false  "페이지 번호"
// @Param        page_size  query     int     false  "페이지당 수량"
// @Param        tag_id     query     string  false  "태그 ID 필터링"
// @Param        keyword    query     string  false  "키워드 검색"
// @Param        file_type  query     string  false  "파일 유형 필터링"
// @Success      200        {object}  map[string]interface{}  "지식 목록"
// @Failure      400        {object}  errors.AppError         "요청 매개변수 오류"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/knowledge [get]
func (h *KnowledgeHandler) ListKnowledge(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start retrieving knowledge list")

	// URL 경로 매개변수에서 지식베이스 ID 가져오기
	kbID := secutils.SanitizeForLog(c.Param("id"))
	if kbID == "" {
		logger.Error(ctx, "Knowledge base ID is empty")
		c.Error(errors.NewBadRequestError("Knowledge base ID cannot be empty"))
		return
	}

	// 쿼리 문자열에서 페이징 매개변수 파싱
	var pagination types.Pagination
	if err := c.ShouldBindQuery(&pagination); err != nil {
		logger.Error(ctx, "Failed to parse pagination parameters", err)
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	tagID := c.Query("tag_id")
	keyword := c.Query("keyword")
	fileType := c.Query("file_type")

	logger.Infof(
		ctx,
		"Retrieving knowledge list under knowledge base, knowledge base ID: %s, tag_id: %s, keyword: %s, file_type: %s, page: %d, page size: %d",
		secutils.SanitizeForLog(kbID),
		secutils.SanitizeForLog(tagID),
		secutils.SanitizeForLog(keyword),
		secutils.SanitizeForLog(fileType),
		pagination.Page,
		pagination.PageSize,
	)

	// 페이징된 지식 항목 검색
	result, err := h.kgService.ListPagedKnowledgeByKnowledgeBaseID(ctx, kbID, &pagination, tagID, keyword, fileType)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(
		ctx,
		"Knowledge list retrieved successfully, knowledge base ID: %s, total: %d",
		secutils.SanitizeForLog(kbID),
		result.Total,
	)
	c.JSON(http.StatusOK, gin.H{
		"success":   true,
		"data":      result.Data,
		"total":     result.Total,
		"page":      result.Page,
		"page_size": result.PageSize,
	})
}

// DeleteKnowledge godoc
// @Summary      지식 삭제
// @Description  ID로 지식 항목 삭제
// @Tags         지식 관리
// @Accept       json
// @Produce      json
// @Param        id   path      string  true  "지식 ID"
// @Success      200  {object}  map[string]interface{}  "삭제 성공"
// @Failure      400  {object}  errors.AppError         "요청 매개변수 오류"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge/{id} [delete]
func (h *KnowledgeHandler) DeleteKnowledge(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start deleting knowledge")

	// URL 경로 매개변수에서 지식 ID 가져오기
	id := secutils.SanitizeForLog(c.Param("id"))
	if id == "" {
		logger.Error(ctx, "Knowledge ID is empty")
		c.Error(errors.NewBadRequestError("Knowledge ID cannot be empty"))
		return
	}

	logger.Infof(ctx, "Deleting knowledge, ID: %s", secutils.SanitizeForLog(id))
	err := h.kgService.DeleteKnowledge(ctx, id)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(ctx, "Knowledge deleted successfully, ID: %s", secutils.SanitizeForLog(id))
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Deleted successfully",
	})
}

// DownloadKnowledgeFile godoc
// @Summary      지식 파일 다운로드
// @Description  지식 항목에 연결된 원본 파일 다운로드
// @Tags         지식 관리
// @Accept       json
// @Produce      application/octet-stream
// @Param        id   path      string  true  "지식 ID"
// @Success      200  {file}    file    "파일 내용"
// @Failure      400  {object}  errors.AppError  "요청 매개변수 오류"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge/{id}/download [get]
func (h *KnowledgeHandler) DownloadKnowledgeFile(c *gin.Context) {
	ctx := c.Request.Context()

	logger.Info(ctx, "Start downloading knowledge file")

	// URL 경로 매개변수에서 지식 ID 가져오기
	id := secutils.SanitizeForLog(c.Param("id"))
	if id == "" {
		logger.Error(ctx, "Knowledge ID is empty")
		c.Error(errors.NewBadRequestError("Knowledge ID cannot be empty"))
		return
	}

	logger.Infof(ctx, "Retrieving knowledge file, ID: %s", secutils.SanitizeForLog(id))

	// 파일 내용 및 파일명 가져오기
	file, filename, err := h.kgService.GetKnowledgeFile(ctx, id)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError("Failed to retrieve file").WithDetails(err.Error()))
		return
	}
	defer file.Close()

	logger.Infof(
		ctx,
		"Knowledge file retrieved successfully, ID: %s, filename: %s",
		secutils.SanitizeForLog(id),
		secutils.SanitizeForLog(filename),
	)

	// 파일 다운로드를 위한 응답 헤더 설정
	c.Header("Content-Description", "File Transfer")
	c.Header("Content-Transfer-Encoding", "binary")
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%s", filename))
	c.Header("Content-Type", "application/octet-stream")
	c.Header("Expires", "0")
	c.Header("Cache-Control", "must-revalidate")
	c.Header("Pragma", "public")

	// 파일 내용을 응답으로 스트리밍
	c.Stream(func(w io.Writer) bool {
		if _, err := io.Copy(w, file); err != nil {
			logger.Errorf(ctx, "Failed to send file: %v", err)
			return false
		}
		logger.Debug(ctx, "File sending completed")
		return false
	})
}

// GetKnowledgeBatchRequest 지식 일괄 검색을 위한 매개변수 정의
type GetKnowledgeBatchRequest struct {
	IDs []string `form:"ids" binding:"required"` // 지식 ID 목록
}

// GetKnowledgeBatch godoc
// @Summary      지식 일괄 가져오기
// @Description  ID 목록을 기반으로 지식 항목 일괄 가져오기
// @Tags         지식 관리
// @Accept       json
// @Produce      json
// @Param        ids  query     []string  true  "지식 ID 목록"
// @Success      200  {object}  map[string]interface{}  "지식 목록"
// @Failure      400  {object}  errors.AppError         "요청 매개변수 오류"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge/batch [get]
func (h *KnowledgeHandler) GetKnowledgeBatch(c *gin.Context) {
	ctx := c.Request.Context()

	// 컨텍스트에서 테넌트 ID 가져오기
	tenantID, ok := c.Get(types.TenantIDContextKey.String())
	if !ok {
		logger.Error(ctx, "Failed to get tenant ID")
		c.Error(errors.NewUnauthorizedError("Unauthorized"))
		return
	}

	// 쿼리 문자열에서 요청 매개변수 파싱
	var req GetKnowledgeBatchRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		c.Error(errors.NewBadRequestError("Invalid request parameters").WithDetails(err.Error()))
		return
	}

	logger.Infof(
		ctx,
		"Batch retrieving knowledge, tenant ID: %d, number of knowledge IDs: %d",
		tenantID, len(req.IDs),
	)

	// 지식 항목 일괄 검색
	knowledges, err := h.kgService.GetKnowledgeBatch(ctx, tenantID.(uint64), req.IDs)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError("Failed to retrieve knowledge list").WithDetails(err.Error()))
		return
	}

	logger.Infof(
		ctx,
		"Batch knowledge retrieval successful, requested count: %d, returned count: %d",
		len(req.IDs), len(knowledges),
	)

	// 결과 반환
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    knowledges,
	})
}

// UpdateKnowledge godoc
// @Summary      지식 업데이트
// @Description  지식 항목 정보 업데이트
// @Tags         지식 관리
// @Accept       json
// @Produce      json
// @Param        id       path      string          true  "지식 ID"
// @Param        request  body      types.Knowledge true  "지식 정보"
// @Success      200      {object}  map[string]interface{}  "업데이트 성공"
// @Failure      400      {object}  errors.AppError         "요청 매개변수 오류"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge/{id} [put]
func (h *KnowledgeHandler) UpdateKnowledge(c *gin.Context) {
	ctx := c.Request.Context()
	// URL 경로 매개변수에서 지식 ID 가져오기
	id := secutils.SanitizeForLog(c.Param("id"))
	if id == "" {
		logger.Error(ctx, "Knowledge ID is empty")
		c.Error(errors.NewBadRequestError("Knowledge ID cannot be empty"))
		return
	}

	var knowledge types.Knowledge
	if err := c.ShouldBindJSON(&knowledge); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	if err := h.kgService.UpdateKnowledge(ctx, &knowledge); err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(ctx, "Knowledge updated successfully, knowledge ID: %s", id)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Knowledge chunk updated successfully",
	})
}

// UpdateManualKnowledge godoc
// @Summary      수동 지식 업데이트
// @Description  수동으로 입력된 Markdown 지식 내용 업데이트
// @Tags         지식 관리
// @Accept       json
// @Produce      json
// @Param        id       path      string                       true  "지식 ID"
// @Param        request  body      types.ManualKnowledgePayload true  "수동 지식 내용"
// @Success      200      {object}  map[string]interface{}       "업데이트된 지식"
// @Failure      400      {object}  errors.AppError              "요청 매개변수 오류"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge/manual/{id} [put]
func (h *KnowledgeHandler) UpdateManualKnowledge(c *gin.Context) {
	ctx := c.Request.Context()
	logger.Info(ctx, "Start updating manual knowledge")

	id := secutils.SanitizeForLog(c.Param("id"))
	if id == "" {
		logger.Error(ctx, "Knowledge ID is empty")
		c.Error(errors.NewBadRequestError("Knowledge ID cannot be empty"))
		return
	}

	var req types.ManualKnowledgePayload
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse manual knowledge update request", err)
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	knowledge, err := h.kgService.UpdateManualKnowledge(ctx, id, &req)
	if err != nil {
		if appErr, ok := errors.IsAppError(err); ok {
			c.Error(appErr)
			return
		}
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"knowledge_id": id,
		})
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(ctx, "Manual knowledge updated successfully, knowledge ID: %s", id)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    knowledge,
	})
}

type knowledgeTagBatchRequest struct {
	Updates map[string]*string `json:"updates" binding:"required,min=1"`
}

// UpdateKnowledgeTagBatch godoc
// @Summary      지식 태그 일괄 업데이트
// @Description  지식 항목의 태그를 일괄 업데이트
// @Tags         지식 관리
// @Accept       json
// @Produce      json
// @Param        request  body      object  true  "태그 업데이트 요청"
// @Success      200      {object}  map[string]interface{}  "업데이트 성공"
// @Failure      400      {object}  errors.AppError         "요청 매개변수 오류"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge/tags [put]
func (h *KnowledgeHandler) UpdateKnowledgeTagBatch(c *gin.Context) {
	ctx := c.Request.Context()
	var req knowledgeTagBatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to parse knowledge tag batch request", err)
		c.Error(errors.NewBadRequestError("요청 매개변수가 유효하지 않습니다").WithDetails(err.Error()))
		return
	}
	if err := h.kgService.UpdateKnowledgeTagBatch(ctx, req.Updates); err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
	})
}

// UpdateImageInfo godoc
// @Summary      이미지 정보 업데이트
// @Description  지식 청크의 이미지 정보 업데이트
// @Tags         지식 관리
// @Accept       json
// @Produce      json
// @Param        id        path      string  true  "지식 ID"
// @Param        chunk_id  path      string  true  "청크 ID"
// @Param        request   body      object{image_info=string}  true  "이미지 정보"
// @Success      200       {object}  map[string]interface{}     "업데이트 성공"
// @Failure      400       {object}  errors.AppError            "요청 매개변수 오류"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge/image/{id}/{chunk_id} [put]
func (h *KnowledgeHandler) UpdateImageInfo(c *gin.Context) {
	ctx := c.Request.Context()
	logger.Info(ctx, "Start updating image info")

	// URL 경로 매개변수에서 지식 ID 가져오기
	id := secutils.SanitizeForLog(c.Param("id"))
	if id == "" {
		logger.Error(ctx, "Knowledge ID is empty")
		c.Error(errors.NewBadRequestError("Knowledge ID cannot be empty"))
		return
	}
	chunkID := secutils.SanitizeForLog(c.Param("chunk_id"))
	if chunkID == "" {
		logger.Error(ctx, "Chunk ID is empty")
		c.Error(errors.NewBadRequestError("Chunk ID cannot be empty"))
		return
	}

	var request struct {
		ImageInfo string `json:"image_info"`
	}

	if err := c.ShouldBindJSON(&request); err != nil {
		logger.Error(ctx, "Failed to parse request parameters", err)
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	// 청크 속성 업데이트
	logger.Infof(ctx, "Updating knowledge chunk, knowledge ID: %s, chunk ID: %s", id, chunkID)
	err := h.kgService.UpdateImageInfo(ctx, id, chunkID, secutils.SanitizeForLog(request.ImageInfo))
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError(err.Error()))
		return
	}

	logger.Infof(ctx, "Knowledge chunk updated successfully, knowledge ID: %s, chunk ID: %s", id, chunkID)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Knowledge chunk image updated successfully",
	})
}

// SearchKnowledge godoc
// @Summary      지식 검색
// @Description  모든 지식베이스에서 키워드로 지식 파일 검색
// @Tags         지식
// @Accept       json
// @Produce      json
// @Param        keyword     query     string  false "검색할 키워드"
// @Param        offset      query     int     false "페이징 오프셋"
// @Param        limit       query     int     false "페이징 제한 (기본값 20)"
// @Param        file_types  query     string  false "필터링할 쉼표로 구분된 파일 확장자 (예: csv,xlsx)"
// @Success      200         {object}  map[string]interface{}     "검색 결과"
// @Failure      400         {object}  errors.AppError            "잘못된 요청"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge/search [get]
func (h *KnowledgeHandler) SearchKnowledge(c *gin.Context) {
	ctx := c.Request.Context()
	keyword := c.Query("keyword")
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))

	// file_types 매개변수 파싱 (쉼표로 구분됨)
	var fileTypes []string
	if fileTypesStr := c.Query("file_types"); fileTypesStr != "" {
		for _, ft := range strings.Split(fileTypesStr, ",") {
			ft = strings.TrimSpace(ft)
			if ft != "" {
				fileTypes = append(fileTypes, ft)
			}
		}
	}

	// 지식 항목 검색 (키워드가 비어 있으면 최신 파일 반환)
	knowledges, hasMore, err := h.kgService.SearchKnowledge(ctx, keyword, offset, limit, fileTypes)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(errors.NewInternalServerError("Failed to search knowledge").WithDetails(err.Error()))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":  true,
		"data":     knowledges,
		"has_more": hasMore,
	})
}
