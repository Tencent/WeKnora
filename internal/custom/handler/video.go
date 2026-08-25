// Package handler 视频列表 / 详情 API（VP-T009 前端列表页 + 详情页数据源）。
package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/custom/model"
)

// VideoHandler 视频列表 / 详情 handler
type VideoHandler struct {
	DB *gorm.DB
}

// NewVideoHandler 构造
func NewVideoHandler(db *gorm.DB) *VideoHandler {
	return &VideoHandler{DB: db}
}

// List 视频列表（按创建时间倒序）
func (h *VideoHandler) List(c *gin.Context) {
	var videos []model.Video
	if err := h.DB.
		Where(
			"((status IN ? AND TRIM(COALESCE(file_url, '')) <> '') OR status = ?)",
			model.VideoInitiallyAvailableStatuses(), model.VideoStatusFailed,
		).
		Order("created_at DESC").
		Find(&videos).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// 返回轻量列表项，避免把完整内容字段全量下发给列表页
	type item struct {
		ID                     string `json:"id"`
		Title                  string `json:"title"`
		VideoType              string `json:"video_type"`
		Status                 string `json:"status"`
		DurationSeconds        int    `json:"duration_seconds"`
		FileURL                string `json:"file_url"`
		ThumbnailURL           string `json:"thumbnail_url"`
		CoverURL               string `json:"cover_url"`
		PlayURL                string `json:"play_url"`
		ProcessingErrorSummary string `json:"processing_error_summary"`
		InitiallyAvailable     bool   `json:"initially_available"`
		CreatedAt              string `json:"created_at"`
	}
	out := make([]item, 0, len(videos))
	for _, v := range videos {
		out = append(out, item{
			ID:                     v.ID,
			Title:                  v.Title,
			VideoType:              v.VideoType,
			Status:                 v.Status,
			DurationSeconds:        v.DurationSeconds,
			FileURL:                v.FileURL,
			ThumbnailURL:           v.ThumbnailURL,
			CoverURL:               v.ThumbnailURL,
			PlayURL:                v.FileURL,
			ProcessingErrorSummary: v.ProcessingErrorSummary,
			InitiallyAvailable:     model.VideoIsInitiallyAvailable(v.Status, v.FileURL, v.ThumbnailURL),
			CreatedAt:              v.CreatedAt.Format("2006-01-02 15:04:05"),
		})
	}
	c.JSON(http.StatusOK, gin.H{"data": out})
}

// Detail 视频详情：完整元数据 + 内容产物状态（5 个 wiki_page_id 是否已生成）
func (h *VideoHandler) Detail(c *gin.Context) {
	id := c.Param("id")
	var v model.Video
	if err := h.DB.First(&v, "id = ?", id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "video not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"data":                videoDetailPayload(v),
		"play_url":            v.FileURL,
		"cover_url":           v.ThumbnailURL,
		"initially_available": model.VideoIsInitiallyAvailable(v.Status, v.FileURL, v.ThumbnailURL),
		"visible_in_list":     model.VideoIsVisibleInList(v.Status, v.FileURL, v.ThumbnailURL),
		"content_status": map[string]bool{
			"knowledge_base":  v.KnowledgeBaseWikiPageID != "",
			"outline":         v.OutlineWikiPageID != "",
			"overview":        v.OverviewWikiPageID != "",
			"summary":         v.SummaryWikiPageID != "",
			"transcript_page": v.TranscriptPageWikiPageID != "",
		},
	})
}

func videoDetailPayload(video model.Video) gin.H {
	return gin.H{
		"id":                           video.ID,
		"title":                        video.Title,
		"video_type":                   video.VideoType,
		"duration_seconds":             video.DurationSeconds,
		"file_url":                     video.FileURL,
		"play_url":                     video.FileURL,
		"thumbnail_url":                video.ThumbnailURL,
		"cover_url":                    video.ThumbnailURL,
		"subtitle_file_url":            video.SubtitleFileURL,
		"transcript_knowledge_id":      video.TranscriptKnowledgeID,
		"transcript_generation":        video.TranscriptGeneration,
		"transcript_revision":          video.TranscriptRevision,
		"transcript_active_revision":   video.TranscriptActiveRevision,
		"knowledge_base_wiki_page_id":  video.KnowledgeBaseWikiPageID,
		"outline_wiki_page_id":         video.OutlineWikiPageID,
		"overview_wiki_page_id":        video.OverviewWikiPageID,
		"summary_wiki_page_id":         video.SummaryWikiPageID,
		"transcript_page_wiki_page_id": video.TranscriptPageWikiPageID,
		"status":                       video.Status,
		"processing_error_summary":     video.ProcessingErrorSummary,
		"uploaded_at":                  video.UploadedAt,
		"ready_at":                     video.ReadyAt,
		"created_at":                   video.CreatedAt,
		"updated_at":                   video.UpdatedAt,
	}
}
