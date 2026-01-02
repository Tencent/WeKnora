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

// TagHandler 지식베이스 태그 작업 처리
type TagHandler struct {
	tagService interfaces.KnowledgeTagService
}

// DeleteTagRequest 태그 삭제 요청 본문
type DeleteTagRequest struct {
	ExcludeIDs []string `json:"exclude_ids"` // 삭제에서 제외할 Chunk ID
}

// NewTagHandler 새로운 TagHandler 생성
func NewTagHandler(tagService interfaces.KnowledgeTagService) *TagHandler {
	return &TagHandler{tagService: tagService}
}

// ListTags godoc
// @Summary      태그 목록 조회
// @Description  지식베이스 아래의 모든 태그 및 통계 정보 조회
// @Tags         태그 관리
// @Accept       json
// @Produce      json
// @Param        id         path      string  true   "지식베이스 ID"
// @Param        page       query     int     false  "페이지 번호"
// @Param        page_size  query     int     false  "페이지당 수량"
// @Param        keyword    query     string  false  "키워드 검색"
// @Success      200        {object}  map[string]interface{}  "태그 목록"
// @Failure      400        {object}  errors.AppError         "요청 매개변수 오류"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/tags [get]
func (h *TagHandler) ListTags(c *gin.Context) {
	ctx := c.Request.Context()
	kbID := secutils.SanitizeForLog(c.Param("id"))

	var page types.Pagination
	if err := c.ShouldBindQuery(&page); err != nil {
		logger.Error(ctx, "Failed to bind pagination query", err)
		c.Error(errors.NewBadRequestError("페이지 매개변수가 유효하지 않습니다").WithDetails(err.Error()))
		return
	}

	keyword := secutils.SanitizeForLog(c.Query("keyword"))

	tags, err := h.tagService.ListTags(ctx, kbID, &page, keyword)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    tags,
	})
}

type createTagRequest struct {
	Name      string `json:"name"       binding:"required"`
	Color     string `json:"color"`
	SortOrder int    `json:"sort_order"`
}

// CreateTag godoc
// @Summary      태그 생성
// @Description  지식베이스 아래에 새 태그 생성
// @Tags         태그 관리
// @Accept       json
// @Produce      json
// @Param        id       path      string  true  "지식베이스 ID"
// @Param        request  body      object{name=string,color=string,sort_order=int}  true  "태그 정보"
// @Success      200      {object}  map[string]interface{}  "생성된 태그"
// @Failure      400      {object}  errors.AppError         "요청 매개변수 오류"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/tags [post]
func (h *TagHandler) CreateTag(c *gin.Context) {
	ctx := c.Request.Context()
	kbID := secutils.SanitizeForLog(c.Param("id"))

	var req createTagRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to bind create tag payload", err)
		c.Error(errors.NewBadRequestError("요청 매개변수가 유효하지 않습니다").WithDetails(err.Error()))
		return
	}

	tag, err := h.tagService.CreateTag(ctx, kbID,
		secutils.SanitizeForLog(req.Name), secutils.SanitizeForLog(req.Color), req.SortOrder)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"kb_id": kbID,
		})
		c.Error(err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    tag,
	})
}

type updateTagRequest struct {
	Name      *string `json:"name"`
	Color     *string `json:"color"`
	SortOrder *int    `json:"sort_order"`
}

// UpdateTag godoc
// @Summary      태그 업데이트
// @Description  태그 정보 업데이트
// @Tags         태그 관리
// @Accept       json
// @Produce      json
// @Param        id       path      string  true  "지식베이스 ID"
// @Param        tag_id   path      string  true  "태그 ID"
// @Param        request  body      object  true  "태그 업데이트 정보"
// @Success      200      {object}  map[string]interface{}  "업데이트된 태그"
// @Failure      400      {object}  errors.AppError         "요청 매개변수 오류"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/tags/{tag_id} [put]
func (h *TagHandler) UpdateTag(c *gin.Context) {
	ctx := c.Request.Context()

	tagID := secutils.SanitizeForLog(c.Param("tag_id"))
	var req updateTagRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to bind update tag payload", err)
		c.Error(errors.NewBadRequestError("요청 매개변수가 유효하지 않습니다").WithDetails(err.Error()))
		return
	}

	tag, err := h.tagService.UpdateTag(ctx, tagID, req.Name, req.Color, req.SortOrder)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"tag_id": tagID,
		})
		c.Error(err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    tag,
	})
}

// DeleteTag godoc
// @Summary      태그 삭제
// @Description  태그 삭제, force=true를 사용하여 참조된 태그 강제 삭제 가능, content_only=true는 태그 아래의 내용만 삭제하고 태그 자체는 유지
// @Tags         태그 관리
// @Accept       json
// @Produce      json
// @Param        id            path      string              true   "지식베이스 ID"
// @Param        tag_id        path      string              true   "태그 ID"
// @Param        force         query     bool                false  "강제 삭제"
// @Param        content_only  query     bool                false  "내용만 삭제, 태그 유지"
// @Param        body          body      DeleteTagRequest    false  "삭제 옵션"
// @Success      200           {object}  map[string]interface{}  "삭제 성공"
// @Failure      400           {object}  errors.AppError         "요청 매개변수 오류"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/tags/{tag_id} [delete]
func (h *TagHandler) DeleteTag(c *gin.Context) {
	ctx := c.Request.Context()
	tagID := secutils.SanitizeForLog(c.Param("tag_id"))

	force := c.Query("force") == "true"
	contentOnly := c.Query("content_only") == "true"

	var req DeleteTagRequest
	// 본문은 선택 사항이므로 바인딩 오류 무시
	_ = c.ShouldBindJSON(&req)

	if err := h.tagService.DeleteTag(ctx, tagID, force, contentOnly, req.ExcludeIDs); err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"tag_id": tagID,
		})
		c.Error(err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
	})
}

// NOTE: TagHandler는 현재 태그 및 통계에 대한 CRUD를 노출합니다.
// 지식/청크 태깅은 전용 지식 및 FAQ API를 통해 처리됩니다.
