package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// LearningHandler serves personal learning requests for authenticated Web users.
type LearningHandler struct{ service interfaces.LearningService }

type updateLearningSettingsRequest struct {
	Enabled *bool `json:"enabled"`
}

type learningOverlayRequest struct {
	KnowledgeBaseID string   `json:"knowledge_base_id"`
	Slugs           []string `json:"slugs"`
}

type prepareLearningQuizRequest struct {
	PageID string `json:"page_id"`
}

// NewLearningHandler creates an HTTP handler for the learning service.
func NewLearningHandler(svc interfaces.LearningService) *LearningHandler {
	return &LearningHandler{service: svc}
}

// GetSettings godoc
// @Summary      获取个人学习设置
// @Description  返回当前 Web 用户的学习开关与算法版本
// @Tags         引导式学习
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "学习设置"
// @Failure      403  {object}  map[string]interface{}  "仅支持当前空间的 Web 用户"
// @Security     Bearer
// @Router       /learning/settings [get]
func (h *LearningHandler) GetSettings(c *gin.Context) {
	data, err := h.service.GetSettings(c.Request.Context())
	h.respond(c, data, err)
}

// SetEnabled godoc
// @Summary      更新个人学习开关
// @Description  开启或关闭当前 Web 用户的个人学习画像
// @Tags         引导式学习
// @Accept       json
// @Produce      json
// @Param        request  body      updateLearningSettingsRequest  true  "学习设置"
// @Success      200      {object}  map[string]interface{}          "更新后的学习设置"
// @Failure      400      {object}  map[string]interface{}          "请求无效"
// @Failure      403      {object}  map[string]interface{}          "身份不支持"
// @Security     Bearer
// @Router       /learning/settings [put]
func (h *LearningHandler) SetEnabled(c *gin.Context) {
	var input updateLearningSettingsRequest
	if !decodeLearningRequest(c, &input) {
		return
	}
	if input.Enabled == nil {
		h.respond(c, nil, types.ErrLearningInvalid)
		return
	}
	data, err := h.service.SetEnabled(c.Request.Context(), *input.Enabled)
	h.respond(c, data, err)
}

// Overview godoc
// @Summary      获取学习概览
// @Description  返回当前用户在指定自有 Wiki 知识库中的学习状态统计
// @Tags         引导式学习
// @Produce      json
// @Param        knowledge_base_id  query     string  true  "知识库 ID"
// @Success      200                {object}  map[string]interface{}  "学习概览"
// @Failure      403                {object}  map[string]interface{}  "学习未开启或无权访问"
// @Security     Bearer
// @Router       /learning/overview [get]
func (h *LearningHandler) Overview(c *gin.Context) {
	data, err := h.service.Overview(c.Request.Context(), c.Query("knowledge_base_id"))
	h.respond(c, data, err)
}

// Node godoc
// @Summary      获取知识节点学习状态
// @Description  返回当前用户对指定 Wiki 页面的熟悉度、掌握度和来源状态
// @Tags         引导式学习
// @Produce      json
// @Param        id   path      string  true  "Wiki 页面 ID"
// @Success      200  {object}  map[string]interface{}  "节点学习状态"
// @Failure      404  {object}  map[string]interface{}  "节点不存在"
// @Security     Bearer
// @Router       /learning/nodes/{id} [get]
func (h *LearningHandler) Node(c *gin.Context) {
	data, err := h.service.Node(c.Request.Context(), c.Param("id"))
	h.respond(c, data, err)
}

// Recommendations godoc
// @Summary      获取学习推荐
// @Description  按复习需求、图邻域、个人兴趣和内容质量返回 Wiki 主题
// @Tags         引导式学习
// @Produce      json
// @Param        knowledge_base_id  query     string  true   "知识库 ID"
// @Param        limit              query     int     false  "返回数量"  default(5)
// @Success      200                {object}  map[string]interface{}  "学习推荐"
// @Failure      400                {object}  map[string]interface{}  "参数无效"
// @Security     Bearer
// @Router       /learning/recommendations [get]
func (h *LearningHandler) Recommendations(c *gin.Context) {
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "5"))
	if err != nil || limit < 1 || limit > 20 {
		h.respond(c, nil, types.ErrLearningInvalid)
		return
	}
	data, err := h.service.Recommend(c.Request.Context(), c.Query("knowledge_base_id"), limit)
	h.respond(c, data, err)
}

// RecordView godoc
// @Summary      记录知识节点阅读
// @Description  将当前用户对指定 Wiki 页面的阅读记录为熟悉度信号，不更新掌握度
// @Tags         引导式学习
// @Produce      json
// @Param        id   path      string  true  "Wiki 页面 ID"
// @Success      200  {object}  map[string]interface{}  "记录成功"
// @Failure      404  {object}  map[string]interface{}  "节点不存在"
// @Security     Bearer
// @Router       /learning/nodes/{id}/view [post]
func (h *LearningHandler) RecordView(c *gin.Context) {
	h.respond(c, nil, h.service.RecordView(c.Request.Context(), c.Param("id")))
}

// Overlay godoc
// @Summary      批量获取知识图谱学习状态
// @Description  返回最多 2000 个 Wiki slug 对应的个人学习状态
// @Tags         引导式学习
// @Accept       json
// @Produce      json
// @Param        request  body      learningOverlayRequest  true  "知识库与 Wiki slug"
// @Success      200      {object}  map[string]interface{}   "节点学习状态列表"
// @Failure      400      {object}  map[string]interface{}   "请求无效"
// @Security     Bearer
// @Router       /learning/overlay [post]
func (h *LearningHandler) Overlay(c *gin.Context) {
	var input learningOverlayRequest
	if !decodeLearningRequest(c, &input) {
		return
	}
	if len(input.Slugs) > 2000 || input.KnowledgeBaseID == "" {
		h.respond(c, nil, types.ErrLearningInvalid)
		return
	}
	data, err := h.service.Overlay(c.Request.Context(), input.KnowledgeBaseID, input.Slugs)
	h.respond(c, data, err)
}

// PrepareQuiz godoc
// @Summary      准备来源测验
// @Description  为指定 Wiki 页面复用或创建异步生成的三题单选测验
// @Tags         引导式学习
// @Accept       json
// @Produce      json
// @Param        request  body      prepareLearningQuizRequest  true  "Wiki 页面"
// @Success      200      {object}  map[string]interface{}      "测验状态"
// @Failure      403      {object}  map[string]interface{}      "学习未开启"
// @Failure      422      {object}  map[string]interface{}      "来源证据不足"
// @Security     Bearer
// @Router       /learning/question-sets [post]
func (h *LearningHandler) PrepareQuiz(c *gin.Context) {
	var input prepareLearningQuizRequest
	if !decodeLearningRequest(c, &input) {
		return
	}
	if input.PageID == "" {
		h.respond(c, nil, types.ErrLearningInvalid)
		return
	}
	data, err := h.service.PrepareQuiz(c.Request.Context(), input.PageID)
	h.respond(c, data, err)
}

// GetQuiz godoc
// @Summary      获取来源测验
// @Description  返回当前用户拥有的测验状态与不含答案的题目
// @Tags         引导式学习
// @Produce      json
// @Param        id   path      string  true  "测验 ID"
// @Success      200  {object}  map[string]interface{}  "测验"
// @Failure      404  {object}  map[string]interface{}  "测验不存在"
// @Security     Bearer
// @Router       /learning/question-sets/{id} [get]
func (h *LearningHandler) GetQuiz(c *gin.Context) {
	data, err := h.service.GetQuiz(c.Request.Context(), c.Param("id"))
	h.respond(c, data, err)
}

// SubmitAnswer godoc
// @Summary      提交测验答案
// @Description  幂等提交一个选项，并在来源仍有效时更新 BKT 掌握度
// @Tags         引导式学习
// @Accept       json
// @Produce      json
// @Param        request  body      types.LearningAnswer    true  "答题请求"
// @Success      200      {object}  map[string]interface{}  "判分结果"
// @Failure      409      {object}  map[string]interface{}  "重复作答或来源已变化"
// @Security     Bearer
// @Router       /learning/attempts [post]
func (h *LearningHandler) SubmitAnswer(c *gin.Context) {
	var input types.LearningAnswer
	if !decodeLearningRequest(c, &input) {
		return
	}
	data, err := h.service.SubmitAnswer(c.Request.Context(), input)
	h.respond(c, data, err)
}

// Export godoc
// @Summary      导出个人学习数据
// @Description  导出当前用户全部或指定知识库的学习画像、测验和作答记录
// @Tags         引导式学习
// @Produce      json
// @Param        knowledge_base_id  query     string  false  "知识库 ID；省略时导出全部"
// @Success      200                {object}  map[string]interface{}  "学习数据导出"
// @Security     Bearer
// @Router       /learning/export [get]
func (h *LearningHandler) Export(c *gin.Context) {
	data, err := h.service.Export(c.Request.Context(), c.Query("knowledge_base_id"))
	h.respond(c, data, err)
}

// Clear godoc
// @Summary      删除个人学习数据
// @Description  删除当前用户全部或指定知识库的学习画像、测验和作答记录
// @Tags         引导式学习
// @Produce      json
// @Param        knowledge_base_id  query     string  false  "知识库 ID；省略时删除全部"
// @Success      200                {object}  map[string]interface{}  "删除计数"
// @Security     Bearer
// @Router       /learning/profile [delete]
func (h *LearningHandler) Clear(c *gin.Context) {
	data, err := h.service.Clear(c.Request.Context(), c.Query("knowledge_base_id"))
	h.respond(c, data, err)
}

func (h *LearningHandler) respond(c *gin.Context, data any, err error) {
	c.Header("Cache-Control", "no-store")
	if err == nil {
		c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
		return
	}
	status, code, message := learningHTTPError(err)
	if status == http.StatusInternalServerError {
		// Provider/database errors may contain answer material. Log a stable
		// code plus request ID, never the wrapped error or payload.
		logger.Warnf(c.Request.Context(), "learning request failed: code=%s", code)
	}
	c.JSON(status, gin.H{"success": false, "error": gin.H{"code": code, "message": message}})
}

func learningHTTPError(err error) (int, string, string) {
	switch {
	case errors.Is(err, types.ErrLearningForbidden):
		return 403, "learning_forbidden", "Learning is available only to Web users in their active workspace."
	case errors.Is(err, types.ErrLearningDisabled):
		return 403, "learning_disabled", "Personal learning is disabled."
	case errors.Is(err, types.ErrLearningNotFound):
		return 404, "learning_not_found", "Learning resource not found."
	case errors.Is(err, types.ErrLearningStale):
		return 409, "learning_stale", "The source changed. Prepare a new quiz."
	case errors.Is(err, types.ErrLearningNotReady):
		return 409, "learning_not_ready", "The quiz is not ready."
	case errors.Is(err, types.ErrLearningConflict):
		return 409, "learning_conflict", "The request conflicts with an earlier submission."
	case errors.Is(err, types.ErrLearningInvalid):
		return 400, "learning_invalid", "Invalid learning request."
	case errors.Is(err, types.ErrLearningBusy):
		return 429, "learning_busy", "Learning generation is busy. Try again later."
	case errors.Is(err, types.ErrLearningEvidence):
		return 422, "learning_evidence", "Not enough current source evidence for a quiz."
	default:
		return 500, "learning_unavailable", "Learning is temporarily unavailable."
	}
}

func decodeLearningRequest(c *gin.Context, target any) bool {
	c.Header("Cache-Control", "no-store")
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 128*1024)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	err := decoder.Decode(target)
	if err == nil {
		var trailing any
		if decoder.Decode(&trailing) != io.EOF {
			err = types.ErrLearningInvalid
		}
	}
	if err != nil {
		c.AbortWithStatusJSON(
			400,
			gin.H{"success": false, "error": gin.H{"code": "learning_invalid", "message": "Invalid learning request."}},
		)
		return false
	}
	return true
}
