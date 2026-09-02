// Package handler 视频列表 / 详情 API（VP-T009 前端列表页 + 详情页数据源）。
package handler

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	objstore "github.com/Tencent/WeKnora/internal/custom/client/minio"
	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
	"github.com/Tencent/WeKnora/internal/custom/model"
)

// VideoHandler 视频列表 / 详情 handler
type VideoHandler struct {
	DB               *gorm.DB
	MinIO            *objstore.Client
	EvidenceWeKnora  *weknora.Client
	KnowledgeWeKnora *weknora.Client
	// WeKnora is retained as a compatibility alias for the evidence client.
	WeKnora *weknora.Client
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
			"uploaded_at IS NOT NULL AND status IN ? AND TRIM(COALESCE(file_url, '')) <> ''",
			append(model.VideoInitiallyAvailableStatuses(), model.VideoStatusFailed),
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
		initiallyAvailable := model.VideoIsVisibleInList(v.Status, v.FileURL, v.ThumbnailURL, v.UploadedAt)
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
			InitiallyAvailable:     initiallyAvailable,
			CreatedAt:              v.CreatedAt.Format("2006-01-02 15:04:05"),
		})
	}
	c.JSON(http.StatusOK, gin.H{"data": out})
}

// Detail 视频详情：完整元数据 + 内容产物状态
func (h *VideoHandler) Detail(c *gin.Context) {
	id := c.Param("id")
	var v model.Video
	if err := h.DB.First(&v, "id = ?", id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "video not found"})
		return
	}
	initiallyAvailable := model.VideoIsVisibleInList(v.Status, v.FileURL, v.ThumbnailURL, v.UploadedAt)
	c.JSON(http.StatusOK, gin.H{
		"data":                videoDetailPayload(v),
		"play_url":            v.FileURL,
		"cover_url":           v.ThumbnailURL,
		"initially_available": initiallyAvailable,
		"visible_in_list":     initiallyAvailable,
		"content_status": map[string]bool{
			"knowledge_base":  v.KnowledgeBaseWikiPageID != "",
			"outline":         v.OutlineWikiPageID != "",
			"summary":         v.SummaryWikiPageID != "",
			"transcript_page": v.TranscriptPageWikiPageID != "",
		},
	})
}

// Delete soft-deletes a video after removing its owned knowledge and media.
func (h *VideoHandler) Delete(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	var video model.Video
	if err := h.DB.First(&video, "id = ?", id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "video not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Transcript chunks belong to the evidence KB, while the standardized full
	// transcript source belongs to the knowledge KB. Keep the clients fixed by
	// role and route source rows by their persisted KB ownership; never delete a
	// source through whichever client happens to be stored in the legacy field.
	evidenceClient := h.EvidenceWeKnora
	if evidenceClient == nil {
		evidenceClient = h.WeKnora
	}
	knowledgeClient := h.KnowledgeWeKnora
	deletions := make(map[*weknora.Client]map[string]struct{})
	addDeletion := func(client *weknora.Client, knowledgeID string) {
		knowledgeID = strings.TrimSpace(knowledgeID)
		if client == nil || knowledgeID == "" {
			return
		}
		if deletions[client] == nil {
			deletions[client] = make(map[string]struct{})
		}
		deletions[client][knowledgeID] = struct{}{}
	}
	addSourceDeletion := func(source model.VideoTranscriptSource) error {
		if evidenceClient == nil && knowledgeClient == nil {
			return nil
		}
		kbID := strings.TrimSpace(source.KnowledgeBaseID)
		if kbID == "" {
			return fmt.Errorf("knowledge_base_routing:source_ownership_missing: source=%s", source.KnowledgeID)
		}
		if evidenceClient != nil && evidenceClient.KBID() == kbID {
			addDeletion(evidenceClient, source.KnowledgeID)
			return nil
		}
		if knowledgeClient != nil && knowledgeClient.KBID() == kbID {
			addDeletion(knowledgeClient, source.KnowledgeID)
			return nil
		}
		return fmt.Errorf("knowledge_base_routing:source_ownership_mismatch: source=%s kb=%s", source.KnowledgeID, kbID)
	}
	addDeletion(evidenceClient, video.TranscriptKnowledgeID)
	if h.DB.Migrator().HasTable(&model.VideoTranscriptChunk{}) {
		var chunks []model.VideoTranscriptChunk
		if err := h.DB.Where("video_id = ?", id).Find(&chunks).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		for _, chunk := range chunks {
			addDeletion(evidenceClient, chunk.KnowledgeID)
		}
	}
	if h.DB.Migrator().HasTable(&model.VideoTranscriptSource{}) {
		var sources []model.VideoTranscriptSource
		if err := h.DB.Where("video_id = ?", id).Find(&sources).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		for _, source := range sources {
			if err := addSourceDeletion(source); err != nil {
				c.JSON(http.StatusBadGateway, gin.H{"error": "delete video knowledge: " + err.Error()})
				return
			}
		}
	}
	clients := make([]*weknora.Client, 0, len(deletions))
	for client := range deletions {
		clients = append(clients, client)
	}
	sort.Slice(clients, func(i, j int) bool { return clients[i].KBID() < clients[j].KBID() })
	for _, client := range clients {
		knowledgeIDs := make([]string, 0, len(deletions[client]))
		for knowledgeID := range deletions[client] {
			knowledgeIDs = append(knowledgeIDs, knowledgeID)
		}
		sort.Strings(knowledgeIDs)
		for _, knowledgeID := range knowledgeIDs {
			if err := client.DeleteKnowledge(c.Request.Context(), knowledgeID); err != nil {
				c.JSON(http.StatusBadGateway, gin.H{"error": "delete video knowledge: " + err.Error()})
				return
			}
		}
	}
	if h.MinIO != nil {
		for _, prefix := range []string{"videos/" + id + "/", "thumbnails/" + id + "/", "subtitles/" + id + "/"} {
			if err := h.MinIO.DeletePrefix(c.Request.Context(), prefix); err != nil {
				c.JSON(http.StatusBadGateway, gin.H{"error": "delete video files: " + err.Error()})
				return
			}
		}
	}

	err := h.DB.Transaction(func(tx *gorm.DB) error {
		if tx.Migrator().HasTable(&model.VideoProcessingJob{}) {
			if err := tx.Where("video_id = ?", id).Delete(&model.VideoProcessingJob{}).Error; err != nil {
				return err
			}
		}
		if tx.Migrator().HasTable(&model.VideoTranscriptChunk{}) {
			if err := tx.Where("video_id = ?", id).Delete(&model.VideoTranscriptChunk{}).Error; err != nil {
				return err
			}
		}
		if tx.Migrator().HasTable(&model.VideoTranscriptSource{}) {
			if err := tx.Where("video_id = ?", id).Delete(&model.VideoTranscriptSource{}).Error; err != nil {
				return err
			}
		}
		return tx.Delete(&video).Error
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "delete video: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "id": id})
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
		"knowledge_audit_status":       video.KnowledgeAuditStatus,
		"outline_wiki_page_id":         video.OutlineWikiPageID,
		"overview_wiki_page_id":        video.OverviewWikiPageID,
		"summary_wiki_page_id":         video.SummaryWikiPageID,
		"summary_wiki_page_version":    video.SummaryWikiPageVersion,
		"summary_source":               video.SummarySource,
		"summary_knowledge_enhanced":   video.SummaryKnowledgeEnhanced,
		"summary_user_edited":          video.SummaryUserEdited,
		"transcript_page_wiki_page_id": video.TranscriptPageWikiPageID,
		"status":                       video.Status,
		"processing_error_summary":     video.ProcessingErrorSummary,
		"uploaded_at":                  video.UploadedAt,
		"ready_at":                     video.ReadyAt,
		"created_at":                   video.CreatedAt,
		"updated_at":                   video.UpdatedAt,
	}
}
