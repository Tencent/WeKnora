package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/application/service/memory"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// MemoryHandler exposes the caller's own long-term memory.
//
// Every route operates on the memory space derived from the request context,
// so no endpoint takes a subject id. That is deliberate: it removes the entire
// class of "can I read another user's memories by changing an id" bugs instead
// of relying on a per-route ownership check.
type MemoryHandler struct {
	memoryService interfaces.MemoryService
}

func NewMemoryHandler(memoryService interfaces.MemoryService) *MemoryHandler {
	return &MemoryHandler{memoryService: memoryService}
}

// GetSettings godoc
// @Summary      获取我的记忆设置
// @Description  返回合并后的记忆开关状态（空间级 + 个人级）与会话记忆条数
// @Tags         长期记忆
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "记忆设置"
// @Security     Bearer
// @Router       /memory/settings [get]
func (h *MemoryHandler) GetSettings(c *gin.Context) {
	ctx := c.Request.Context()
	settings, err := h.memoryService.GetSettings(ctx)
	if err != nil {
		h.fail(c, err, "Failed to load memory settings")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": settings})
}

type updateMemorySettingsRequest struct {
	Enabled *bool `json:"enabled"`
}

// UpdateSettings godoc
// @Summary      更新我的记忆设置
// @Description  开启或关闭当前用户自己的长期记忆
// @Tags         长期记忆
// @Accept       json
// @Produce      json
// @Param        request  body      object  true  "设置"
// @Success      200      {object}  map[string]interface{}  "更新后的设置"
// @Security     Bearer
// @Router       /memory/settings [put]
func (h *MemoryHandler) UpdateSettings(c *gin.Context) {
	ctx := c.Request.Context()
	var req updateMemorySettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}
	if req.Enabled == nil {
		c.Error(apperrors.NewBadRequestError("enabled is required"))
		return
	}
	if err := h.memoryService.SetEnabled(ctx, *req.Enabled); err != nil {
		h.fail(c, err, "Failed to update memory settings")
		return
	}
	settings, err := h.memoryService.GetSettings(ctx)
	if err != nil {
		h.fail(c, err, "Failed to load memory settings")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": settings})
}

// ---------------------------------------------------------------------------
// Profile
// ---------------------------------------------------------------------------

// GetProfile godoc
// @Summary      获取我的记忆画像
// @Description  返回每轮对话都会注入的常驻画像，尚未生成时返回 null
// @Tags         长期记忆
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "记忆画像"
// @Security     Bearer
// @Router       /memory/profile [get]
func (h *MemoryHandler) GetProfile(c *gin.Context) {
	ctx := c.Request.Context()
	profile, err := h.memoryService.Profile(ctx)
	if err != nil {
		h.fail(c, err, "Failed to load memory profile")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": profile})
}

type saveMemoryProfileRequest struct {
	Body string `json:"body"`
}

// SaveProfile godoc
// @Summary      修改我的记忆画像
// @Description  用用户自己写的内容替换常驻画像，后台整理时会保留这次修改
// @Tags         长期记忆
// @Accept       json
// @Produce      json
// @Param        request  body      object  true  "画像正文"
// @Success      200      {object}  map[string]interface{}  "画像版本号"
// @Security     Bearer
// @Router       /memory/profile [put]
func (h *MemoryHandler) SaveProfile(c *gin.Context) {
	ctx := c.Request.Context()
	var req saveMemoryProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}
	revision, err := h.memoryService.SaveProfile(ctx, req.Body)
	if err != nil {
		h.fail(c, err, "Failed to save memory profile")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"revision": revision}})
}

// DeleteProfile godoc
// @Summary      删除我的记忆画像
// @Description  清空常驻画像，会话记忆保留，下次后台整理会重新生成
// @Tags         长期记忆
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "删除成功"
// @Security     Bearer
// @Router       /memory/profile [delete]
func (h *MemoryHandler) DeleteProfile(c *gin.Context) {
	ctx := c.Request.Context()
	if err := h.memoryService.DeleteProfile(ctx); err != nil {
		h.fail(c, err, "Failed to delete memory profile")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ---------------------------------------------------------------------------
// Episodes
// ---------------------------------------------------------------------------

// ListEpisodes godoc
// @Summary      列出我的会话记忆
// @Description  分页返回每段对话的记忆档案，按时间倒序
// @Tags         长期记忆
// @Produce      json
// @Param        limit   query     int  false  "每页条数"  default(20)
// @Param        offset  query     int  false  "偏移量"
// @Success      200     {object}  map[string]interface{}  "会话记忆列表"
// @Security     Bearer
// @Router       /memory/episodes [get]
func (h *MemoryHandler) ListEpisodes(c *gin.Context) {
	ctx := c.Request.Context()
	limit, offset := memoryListPaging(c)
	episodes, total, err := h.memoryService.ListEpisodes(ctx, limit, offset)
	if err != nil {
		h.fail(c, err, "Failed to list memory episodes")
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    episodes,
		"total":   total,
	})
}

// GetEpisode godoc
// @Summary      读取一段会话记忆
// @Description  返回一段对话的完整记忆档案
// @Tags         长期记忆
// @Produce      json
// @Param        id   path      string  true  "会话记忆 ID"
// @Success      200  {object}  map[string]interface{}  "会话记忆"
// @Security     Bearer
// @Router       /memory/episodes/{id} [get]
func (h *MemoryHandler) GetEpisode(c *gin.Context) {
	ctx := c.Request.Context()
	episode, err := h.memoryService.GetEpisode(ctx, c.Param("id"))
	if err != nil {
		h.fail(c, err, "Failed to load memory episode")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": episode})
}

// DeleteEpisode godoc
// @Summary      删除一段会话记忆
// @Description  永久删除一段对话的记忆档案
// @Tags         长期记忆
// @Produce      json
// @Param        id   path      string  true  "会话记忆 ID"
// @Success      200  {object}  map[string]interface{}  "删除成功"
// @Security     Bearer
// @Router       /memory/episodes/{id} [delete]
func (h *MemoryHandler) DeleteEpisode(c *gin.Context) {
	ctx := c.Request.Context()
	if err := h.memoryService.DeleteEpisode(ctx, c.Param("id")); err != nil {
		h.fail(c, err, "Failed to delete memory episode")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ---------------------------------------------------------------------------
// Notes
// ---------------------------------------------------------------------------

// ListNotes godoc
// @Summary      列出我要求记住的内容
// @Description  返回用户明确要求记住的原话，按时间倒序
// @Tags         长期记忆
// @Produce      json
// @Param        limit  query     int  false  "条数上限"
// @Success      200    {object}  map[string]interface{}  "记忆列表"
// @Security     Bearer
// @Router       /memory/notes [get]
func (h *MemoryHandler) ListNotes(c *gin.Context) {
	ctx := c.Request.Context()
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", strconv.Itoa(types.MemoryNotesMaxItems)))
	if limit <= 0 || limit > types.MemoryNotesMaxItems {
		limit = types.MemoryNotesMaxItems
	}
	notes, err := h.memoryService.ListNotes(ctx, limit)
	if err != nil {
		h.fail(c, err, "Failed to list memory notes")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": notes})
}

type createMemoryNoteRequest struct {
	Content string `json:"content"`
}

// CreateNote godoc
// @Summary      新增一条我要求记住的内容
// @Description  手动添加一条原话记忆，下一轮对话即生效
// @Tags         长期记忆
// @Accept       json
// @Produce      json
// @Param        request  body      object  true  "记忆内容"
// @Success      200      {object}  map[string]interface{}  "新增的记忆"
// @Security     Bearer
// @Router       /memory/notes [post]
func (h *MemoryHandler) CreateNote(c *gin.Context) {
	ctx := c.Request.Context()
	var req createMemoryNoteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewValidationError("Invalid request data").WithDetails(err.Error()))
		return
	}
	note, err := h.memoryService.AddNote(ctx, req.Content)
	if err != nil {
		h.fail(c, err, "Failed to create memory note")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": note})
}

// DeleteNote godoc
// @Summary      删除一条我要求记住的内容
// @Description  永久删除一条原话记忆
// @Tags         长期记忆
// @Produce      json
// @Param        id   path      string  true  "记忆 ID"
// @Success      200  {object}  map[string]interface{}  "删除成功"
// @Security     Bearer
// @Router       /memory/notes/{id} [delete]
func (h *MemoryHandler) DeleteNote(c *gin.Context) {
	ctx := c.Request.Context()
	if err := h.memoryService.DeleteNote(ctx, c.Param("id")); err != nil {
		h.fail(c, err, "Failed to delete memory note")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ---------------------------------------------------------------------------
// Clear and export
// ---------------------------------------------------------------------------

const (
	// memoryExportPageSize is how many rows one export page reads.
	memoryExportPageSize = 500
	// memoryExportMaxEpisodes bounds a single export so one enormous store
	// cannot turn a download into an unbounded read.
	memoryExportMaxEpisodes = 20000
	// memoryEpisodePageMax bounds one page of the accounts list. An account
	// carries a full narrative summary, so a page of them is orders of
	// magnitude heavier than a page of rows used to be.
	memoryEpisodePageMax = 100
)

func memoryListPaging(c *gin.Context) (limit, offset int) {
	limit, _ = strconv.Atoi(c.DefaultQuery("limit", "20"))
	if limit <= 0 || limit > memoryEpisodePageMax {
		limit = 20
	}
	offset, _ = strconv.Atoi(c.DefaultQuery("offset", "0"))
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

// Clear godoc
// @Summary      清空我的记忆
// @Description  永久删除当前用户的常驻画像、全部会话记忆与原话记忆
// @Tags         长期记忆
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "清空成功"
// @Security     Bearer
// @Router       /memory/all [delete]
func (h *MemoryHandler) Clear(c *gin.Context) {
	ctx := c.Request.Context()
	removed, err := h.memoryService.Clear(ctx)
	if err != nil {
		h.fail(c, err, "Failed to clear memories")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "removed": removed})
}

// Export godoc
// @Summary      导出我的记忆
// @Description  以 JSON 导出当前用户的常驻画像、会话记忆与原话记忆
// @Tags         长期记忆
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "记忆导出"
// @Security     Bearer
// @Router       /memory/export [get]
func (h *MemoryHandler) Export(c *gin.Context) {
	ctx := c.Request.Context()
	profile, err := h.memoryService.Profile(ctx)
	if err != nil {
		h.fail(c, err, "Failed to export memories")
		return
	}
	// Export is a snapshot, not a page, so it walks the accounts to the end.
	// A store holds far more accounts than one page of the manager shows, and
	// a download that silently returned the first page would look complete.
	var episodes []*types.MemoryEpisode
	var episodeTotal int64
	for {
		page, pageTotal, err := h.memoryService.ListEpisodes(ctx, memoryExportPageSize, len(episodes))
		if err != nil {
			h.fail(c, err, "Failed to export memories")
			return
		}
		episodeTotal = pageTotal
		episodes = append(episodes, page...)
		if len(page) < memoryExportPageSize || int64(len(episodes)) >= episodeTotal {
			break
		}
		if len(episodes) >= memoryExportMaxEpisodes {
			break
		}
	}
	notes, err := h.memoryService.ListNotes(ctx, types.MemoryNotesMaxItems)
	if err != nil {
		h.fail(c, err, "Failed to export memories")
		return
	}
	total := episodeTotal + int64(len(notes))
	if profile != nil {
		total++
	}
	c.Header("Content-Disposition", `attachment; filename="weknora-memories.json"`)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"total":   total,
		// Say so rather than letting a partial file look complete. Only the
		// safety ceiling can trigger this, so it stays false in practice.
		"truncated": int64(len(episodes)) < episodeTotal,
		"data": gin.H{
			"profile":  profile,
			"episodes": episodes,
			"notes":    notes,
		},
	})
}

// Consolidate godoc
// @Summary      立刻整理我的记忆
// @Description  立刻用最近的会话记忆重写一次常驻画像，不等待后台整理
// @Tags         长期记忆
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "整理结果"
// @Security     Bearer
// @Router       /memory/consolidate [post]
func (h *MemoryHandler) Consolidate(c *gin.Context) {
	ctx := c.Request.Context()
	result, err := h.memoryService.ConsolidateNow(ctx)
	if err != nil {
		h.fail(c, err, "Failed to consolidate memories")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}

// fail maps service errors onto HTTP responses. A missing record and one
// belonging to someone else produce the same 404 on purpose.
func (h *MemoryHandler) fail(c *gin.Context, err error, message string) {
	switch {
	case errors.Is(err, memory.ErrNoMemoryScope):
		c.Error(apperrors.NewUnauthorizedError("no principal in request"))
	case errors.Is(err, memory.ErrNotFound):
		c.Error(apperrors.NewNotFoundError("memory not found"))
	case errors.Is(err, memory.ErrSensitiveContent),
		errors.Is(err, memory.ErrContentTooLong),
		errors.Is(err, memory.ErrEmptyContent),
		errors.Is(err, memory.ErrNotesFull):
		c.Error(apperrors.NewBadRequestError(err.Error()))
	case errors.Is(err, memory.ErrMemoryDisabled):
		c.Error(apperrors.NewBadRequestError("memory is disabled"))
	default:
		logger.ErrorWithFields(c.Request.Context(), err, nil)
		c.Error(apperrors.NewInternalServerError(message).WithDetails(err.Error()))
	}
}
