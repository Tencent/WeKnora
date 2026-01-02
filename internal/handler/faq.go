package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// FAQHandler FAQ 지식베이스 작업을 처리합니다.
type FAQHandler struct {
	knowledgeService interfaces.KnowledgeService
}

// NewFAQHandler 새로운 FAQ 핸들러 생성
func NewFAQHandler(knowledgeService interfaces.KnowledgeService) *FAQHandler {
	return &FAQHandler{knowledgeService: knowledgeService}
}

// ListEntries godoc
// @Summary      FAQ 항목 목록 조회
// @Description  지식베이스 하의 FAQ 항목 목록 조회, 페이징 및 필터링 지원
// @Tags         FAQ 관리
// @Accept       json
// @Produce      json
// @Param        id           path      string  true   "지식베이스 ID"
// @Param        page         query     int     false  "페이지 번호"
// @Param        page_size    query     int     false  "페이지당 항목 수"
// @Param        tag_id       query     string  false  "태그 ID 필터"
// @Param        keyword      query     string  false  "키워드 검색"
// @Param        search_field query     string  false  "검색 필드: standard_question(표준 질문), similar_questions(유사 질문), answers(답변), 기본값은 전체 검색"
// @Param        sort_order   query     string  false  "정렬 방식: asc(업데이트 시간 오름차순), 기본값은 업데이트 시간 내림차순"
// @Success      200        {object}  map[string]interface{}  "FAQ 목록"
// @Failure      400        {object}  errors.AppError         "요청 매개변수 오류"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/faq/entries [get]
func (h *FAQHandler) ListEntries(c *gin.Context) {
	ctx := c.Request.Context()
	var page types.Pagination
	if err := c.ShouldBindQuery(&page); err != nil {
		logger.Error(ctx, "Failed to bind pagination query", err)
		c.Error(errors.NewBadRequestError("Invalid pagination parameters").WithDetails(err.Error()))
		return
	}

	tagID := secutils.SanitizeForLog(c.Query("tag_id"))
	keyword := secutils.SanitizeForLog(c.Query("keyword"))
	searchField := secutils.SanitizeForLog(c.Query("search_field"))
	sortOrder := secutils.SanitizeForLog(c.Query("sort_order"))

	result, err := h.knowledgeService.ListFAQEntries(ctx, secutils.SanitizeForLog(c.Param("id")), &page, tagID, keyword, searchField, sortOrder)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    result,
	})
}

// UpsertEntries godoc
// @Summary      FAQ 항목 일괄 업데이트/삽입
// @Description  FAQ 항목을 비동기로 일괄 업데이트하거나 삽입
// @Tags         FAQ 관리
// @Accept       json
// @Produce      json
// @Param        id       path      string                    true  "지식베이스 ID"
// @Param        request  body      types.FAQBatchUpsertPayload  true  "일괄 작업 요청"
// @Success      200      {object}  map[string]interface{}    "작업 ID"
// @Failure      400      {object}  errors.AppError           "요청 매개변수 오류"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/faq/entries [post]
func (h *FAQHandler) UpsertEntries(c *gin.Context) {
	ctx := c.Request.Context()
	var req types.FAQBatchUpsertPayload
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to bind FAQ upsert payload", err)
		c.Error(errors.NewBadRequestError("Invalid request parameters").WithDetails(err.Error()))
		return
	}

	taskID, err := h.knowledgeService.UpsertFAQEntries(ctx, secutils.SanitizeForLog(c.Param("id")), &req)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"task_id": taskID,
		},
	})
}

// CreateEntry godoc
// @Summary      단일 FAQ 항목 생성
// @Description  단일 FAQ 항목 동기 생성
// @Tags         FAQ 관리
// @Accept       json
// @Produce      json
// @Param        id       path      string                true  "지식베이스 ID"
// @Param        request  body      types.FAQEntryPayload true  "FAQ 항목"
// @Success      200      {object}  map[string]interface{}  "생성된 FAQ 항목"
// @Failure      400      {object}  errors.AppError         "요청 매개변수 오류"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/faq/entry [post]
func (h *FAQHandler) CreateEntry(c *gin.Context) {
	ctx := c.Request.Context()
	var req types.FAQEntryPayload
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to bind FAQ entry payload", err)
		c.Error(errors.NewBadRequestError("Invalid request parameters").WithDetails(err.Error()))
		return
	}

	entry, err := h.knowledgeService.CreateFAQEntry(ctx, secutils.SanitizeForLog(c.Param("id")), &req)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    entry,
	})
}

// UpdateEntry godoc
// @Summary      FAQ 항목 업데이트
// @Description  지정된 FAQ 항목 업데이트
// @Tags         FAQ 관리
// @Accept       json
// @Produce      json
// @Param        id        path      string                true  "지식베이스 ID"
// @Param        entry_id  path      string                true  "FAQ 항목 ID"
// @Param        request   body      types.FAQEntryPayload true  "FAQ 항목"
// @Success      200       {object}  map[string]interface{}  "업데이트 성공"
// @Failure      400       {object}  errors.AppError         "요청 매개변수 오류"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/faq/entries/{entry_id} [put]
func (h *FAQHandler) UpdateEntry(c *gin.Context) {
	ctx := c.Request.Context()
	var req types.FAQEntryPayload
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to bind FAQ entry payload", err)
		c.Error(errors.NewBadRequestError("Invalid request parameters").WithDetails(err.Error()))
		return
	}

	if err := h.knowledgeService.UpdateFAQEntry(ctx,
		secutils.SanitizeForLog(c.Param("id")), secutils.SanitizeForLog(c.Param("entry_id")), &req); err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
	})
}

// UpdateEntryTagBatch godoc
// @Summary      FAQ 태그 일괄 업데이트
// @Description  FAQ 항목의 태그 일괄 업데이트
// @Tags         FAQ 관리
// @Accept       json
// @Produce      json
// @Param        id       path      string  true  "지식베이스 ID"
// @Param        request  body      object  true  "태그 업데이트 요청"
// @Success      200      {object}  map[string]interface{}  "업데이트 성공"
// @Failure      400      {object}  errors.AppError         "요청 매개변수 오류"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/faq/entries/tags [put]
func (h *FAQHandler) UpdateEntryTagBatch(c *gin.Context) {
	ctx := c.Request.Context()
	var req faqEntryTagBatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to bind FAQ entry tag batch payload", err)
		c.Error(errors.NewBadRequestError("Invalid request parameters").WithDetails(err.Error()))
		return
	}
	if err := h.knowledgeService.UpdateFAQEntryTagBatch(ctx,
		secutils.SanitizeForLog(c.Param("id")), req.Updates); err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
	})
}

// UpdateEntryFieldsBatch godoc
// @Summary      FAQ 필드 일괄 업데이트
// @Description  FAQ 항목의 여러 필드 일괄 업데이트 (is_enabled, is_recommended, tag_id)
// @Tags         FAQ 관리
// @Accept       json
// @Produce      json
// @Param        id       path      string                        true  "지식베이스 ID"
// @Param        request  body      types.FAQEntryFieldsBatchUpdate  true  "필드 업데이트 요청"
// @Success      200      {object}  map[string]interface{}        "업데이트 성공"
// @Failure      400      {object}  errors.AppError               "요청 매개변수 오류"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/faq/entries/fields [put]
func (h *FAQHandler) UpdateEntryFieldsBatch(c *gin.Context) {
	ctx := c.Request.Context()
	var req types.FAQEntryFieldsBatchUpdate
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to bind FAQ entry fields batch payload", err)
		c.Error(errors.NewBadRequestError("Invalid request parameters").WithDetails(err.Error()))
		return
	}
	if err := h.knowledgeService.UpdateFAQEntryFieldsBatch(ctx,
		secutils.SanitizeForLog(c.Param("id")), &req); err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
	})
}

// faqDeleteRequest FAQ 항목 일괄 삭제를 위한 요청
type faqDeleteRequest struct {
	IDs []string `json:"ids" binding:"required,min=1,dive,required"`
}

// faqEntryTagBatchRequest FAQ 항목 태그 일괄 업데이트를 위한 요청
type faqEntryTagBatchRequest struct {
	Updates map[string]*string `json:"updates" binding:"required,min=1"`
}

// DeleteEntries godoc
// @Summary      FAQ 항목 일괄 삭제
// @Description  지정된 FAQ 항목 일괄 삭제
// @Tags         FAQ 관리
// @Accept       json
// @Produce      json
// @Param        id       path      string  true  "지식베이스 ID"
// @Param        request  body      object{ids=[]string}  true  "삭제할 FAQ ID 목록"
// @Success      200      {object}  map[string]interface{}  "삭제 성공"
// @Failure      400      {object}  errors.AppError         "요청 매개변수 오류"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/faq/entries [delete]
func (h *FAQHandler) DeleteEntries(c *gin.Context) {
	ctx := c.Request.Context()
	var req faqDeleteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Errorf(ctx, "Failed to bind FAQ delete payload: %s", secutils.SanitizeForLog(err.Error()))
		c.Error(errors.NewBadRequestError("Invalid request parameters").WithDetails(err.Error()))
		return
	}

	if err := h.knowledgeService.DeleteFAQEntries(ctx,
		secutils.SanitizeForLog(c.Param("id")),
		secutils.SanitizeForLogArray(req.IDs)); err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
	})
}

// SearchFAQ godoc
// @Summary      FAQ 검색
// @Description  하이브리드 검색을 사용하여 FAQ 검색, 2단계 우선순위 태그 리콜 지원: first_priority_tag_ids가 가장 높은 우선순위, second_priority_tag_ids가 그 다음
// @Tags         FAQ 관리
// @Accept       json
// @Produce      json
// @Param        id       path      string                true  "지식베이스 ID"
// @Param        request  body      types.FAQSearchRequest  true  "검색 요청"
// @Success      200      {object}  map[string]interface{}  "검색 결과"
// @Failure      400      {object}  errors.AppError         "요청 매개변수 오류"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/faq/search [post]
func (h *FAQHandler) SearchFAQ(c *gin.Context) {
	ctx := c.Request.Context()
	var req types.FAQSearchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to bind FAQ search payload", err)
		c.Error(errors.NewBadRequestError("Invalid request parameters").WithDetails(err.Error()))
		return
	}
	req.QueryText = secutils.SanitizeForLog(req.QueryText)
	if req.MatchCount <= 0 {
		req.MatchCount = 10
	}
	if req.MatchCount > 200 {
		req.MatchCount = 200
	}
	entries, err := h.knowledgeService.SearchFAQEntries(ctx, secutils.SanitizeForLog(c.Param("id")), &req)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    entries,
	})
}

// ExportEntries godoc
// @Summary      FAQ 항목 내보내기
// @Description  모든 FAQ 항목을 CSV 파일로 내보내기
// @Tags         FAQ 관리
// @Accept       json
// @Produce      text/csv
// @Param        id   path      string  true  "지식베이스 ID"
// @Success      200  {file}    file    "CSV 파일"
// @Failure      400  {object}  errors.AppError  "요청 매개변수 오류"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/faq/entries/export [get]
func (h *FAQHandler) ExportEntries(c *gin.Context) {
	ctx := c.Request.Context()
	kbID := secutils.SanitizeForLog(c.Param("id"))

	csvData, err := h.knowledgeService.ExportFAQEntries(ctx, kbID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(err)
		return
	}

	// CSV 다운로드를 위한 응답 헤더 설정
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename=faq_export.csv")
	// UTF-8 호환성을 위한 BOM 추가
	bom := []byte{0xEF, 0xBB, 0xBF}
	c.Data(http.StatusOK, "text/csv; charset=utf-8", append(bom, csvData...))
}

// GetEntry godoc
// @Summary      FAQ 항목 상세 정보 조회
// @Description  ID를 기반으로 단일 FAQ 항목의 상세 정보 조회
// @Tags         FAQ 관리
// @Accept       json
// @Produce      json
// @Param        id        path      string  true  "지식베이스 ID"
// @Param        entry_id  path      string  true  "FAQ 항목 ID"
// @Success      200       {object}  map[string]interface{}  "FAQ 항목 상세 정보"
// @Failure      400       {object}  errors.AppError         "요청 매개변수 오류"
// @Failure      404       {object}  errors.AppError         "항목을 찾을 수 없음"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/faq/entries/{entry_id} [get]
func (h *FAQHandler) GetEntry(c *gin.Context) {
	ctx := c.Request.Context()
	kbID := secutils.SanitizeForLog(c.Param("id"))
	entryID := secutils.SanitizeForLog(c.Param("entry_id"))

	entry, err := h.knowledgeService.GetFAQEntry(ctx, kbID, entryID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    entry,
	})
}

// GetImportProgress godoc
// @Summary      FAQ 가져오기 진행 상황 조회
// @Description  FAQ 가져오기 작업의 진행 상황 조회
// @Tags         FAQ 관리
// @Accept       json
// @Produce      json
// @Param        task_id  path      string  true  "작업 ID"
// @Success      200      {object}  map[string]interface{}  "가져오기 진행 상황"
// @Failure      404      {object}  errors.AppError         "작업을 찾을 수 없음"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /faq/import/progress/{task_id} [get]
func (h *FAQHandler) GetImportProgress(c *gin.Context) {
	ctx := c.Request.Context()
	taskID := secutils.SanitizeForLog(c.Param("task_id"))

	progress, err := h.knowledgeService.GetFAQImportProgress(ctx, taskID)
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
