package handler

import (
	"net/http"
	"strconv"
	"strings"

	werrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

type ProcessingDashboardHandler struct {
	service interfaces.ProcessingDashboardService
}

func NewProcessingDashboardHandler(service interfaces.ProcessingDashboardService) *ProcessingDashboardHandler {
	return &ProcessingDashboardHandler{service: service}
}

// GetDashboard godoc
// @Summary      获取知识处理看板
// @Description  只读返回九个逻辑阶段的当前处理、排队和重试聚合。
// @Tags         知识处理看板
// @Produce      json
// @Param        kb_id        query  string  false  "知识库 ID"
// @Param        keyword      query  string  false  "标题或文件名关键词"
// @Param        active_limit query  int     false  "每阶段运行预览数量，1-10"
// @Success      200          {object}  map[string]interface{}
// @Router       /api/v1/knowledge-processing/dashboard [get]
func (h *ProcessingDashboardHandler) GetDashboard(c *gin.Context) {
	filter := h.filterFromContext(c)
	if v := strings.TrimSpace(c.Query("active_limit")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			c.Error(werrors.NewBadRequestError("invalid active_limit"))
			return
		}
		filter.ActivePreviewLimit = n
	}
	resp, err := h.service.GetDashboard(c.Request.Context(), filter)
	if err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": resp})
}

// ListStageItems godoc
// @Summary      分页获取阶段队列
// @Description  只读分页返回某个逻辑阶段的 running/queued/retrying 知识。
// @Tags         知识处理看板
// @Produce      json
// @Param        stage      path   string  true   "逻辑阶段"
// @Param        state      query  string  true   "running|queued|retrying"
// @Param        cursor     query  string  false  "游标"
// @Param        page_size  query  int     false  "页大小，默认 20，最大 100"
// @Success      200        {object}  map[string]interface{}
// @Router       /api/v1/knowledge-processing/stages/{stage}/items [get]
func (h *ProcessingDashboardHandler) ListStageItems(c *gin.Context) {
	stage, ok := parseProcessingStage(c.Param("stage"))
	if !ok {
		c.Error(werrors.NewBadRequestError("invalid stage"))
		return
	}
	state, ok := parseProcessingQueueState(c.Query("state"))
	if !ok {
		c.Error(werrors.NewBadRequestError("invalid state"))
		return
	}
	pageSize := 20
	if v := strings.TrimSpace(c.Query("page_size")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			c.Error(werrors.NewBadRequestError("invalid page_size"))
			return
		}
		pageSize = n
	}
	resp, err := h.service.ListStageItems(c.Request.Context(), h.filterFromContext(c), stage, state, c.Query("cursor"), pageSize)
	if err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": resp})
}

// GetKnowledgeProcessingDetail godoc
// @Summary      获取单篇知识处理详情
// @Description  只读返回当前 attempt 的九阶段逻辑状态与原始 span tree。
// @Tags         知识处理看板
// @Produce      json
// @Param        id       path   string  true   "知识 ID"
// @Param        attempt  query  int     false  "attempt"
// @Success      200      {object}  map[string]interface{}
// @Router       /api/v1/knowledge-processing/knowledge/{id} [get]
func (h *ProcessingDashboardHandler) GetKnowledgeProcessingDetail(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		c.Error(werrors.NewBadRequestError("knowledge id is required"))
		return
	}
	attempt := 0
	if v := strings.TrimSpace(c.Query("attempt")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			c.Error(werrors.NewBadRequestError("invalid attempt"))
			return
		}
		attempt = n
	}
	resp, err := h.service.GetKnowledgeProcessingDetail(c.Request.Context(), id, attempt)
	if err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": resp})
}

func (h *ProcessingDashboardHandler) filterFromContext(c *gin.Context) types.ProcessingDashboardFilter {
	return types.ProcessingDashboardFilter{
		TenantID:           c.GetUint64(types.TenantIDContextKey.String()),
		UserID:             stringFromGin(c, types.UserIDContextKey.String()),
		KnowledgeBaseID:    strings.TrimSpace(c.Query("kb_id")),
		Keyword:            strings.TrimSpace(c.Query("keyword")),
		ActivePreviewLimit: 5,
	}
}

func stringFromGin(c *gin.Context, key string) string {
	v, ok := c.Get(key)
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

func parseProcessingStage(raw string) (types.ProcessingLogicalStage, bool) {
	stage := types.ProcessingLogicalStage(strings.TrimSpace(raw))
	for _, candidate := range types.ProcessingLogicalStages {
		if candidate == stage {
			return stage, true
		}
	}
	return "", false
}

func parseProcessingQueueState(raw string) (types.ProcessingStageState, bool) {
	switch types.ProcessingStageState(strings.TrimSpace(raw)) {
	case types.ProcessingStateRunning:
		return types.ProcessingStateRunning, true
	case types.ProcessingStateQueued:
		return types.ProcessingStateQueued, true
	case types.ProcessingStateRetrying:
		return types.ProcessingStateRetrying, true
	default:
		return "", false
	}
}
