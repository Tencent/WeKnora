package handler

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/custom/model"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type ChatEvidenceHandler struct {
	db *gorm.DB
}

type ChatEvidenceItem struct {
	KnowledgeID string `json:"knowledge_id"`
	VideoID     string `json:"video_id"`
	VideoTitle  string `json:"video_title"`
	VideoCover  string `json:"video_cover_url"`
	Seconds     int    `json:"seconds"`
	Timestamp   string `json:"timestamp"`
}

func NewChatEvidenceHandler(db *gorm.DB) *ChatEvidenceHandler {
	return &ChatEvidenceHandler{db: db}
}

func (h *ChatEvidenceHandler) Lookup(c *gin.Context) {
	ids := splitQueryValues(c.Query("knowledge_ids"))
	if len(ids) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "knowledge_ids is required"})
		return
	}

	var chunks []model.VideoTranscriptChunk
	if err := h.db.WithContext(c.Request.Context()).Where("knowledge_id IN ?", ids).Find(&chunks).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if len(chunks) == 0 {
		c.JSON(http.StatusOK, gin.H{"data": []ChatEvidenceItem{}})
		return
	}

	videoIDs := make([]string, 0, len(chunks))
	seenVideoIDs := map[string]struct{}{}
	for _, chunk := range chunks {
		if chunk.VideoID == "" {
			continue
		}
		if _, exists := seenVideoIDs[chunk.VideoID]; exists {
			continue
		}
		seenVideoIDs[chunk.VideoID] = struct{}{}
		videoIDs = append(videoIDs, chunk.VideoID)
	}
	var videos []model.Video
	if len(videoIDs) > 0 {
		if err := h.db.WithContext(c.Request.Context()).Where("id IN ?", videoIDs).Find(&videos).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	videoByID := make(map[string]model.Video, len(videos))
	for _, video := range videos {
		videoByID[video.ID] = video
	}

	items := make([]ChatEvidenceItem, 0, len(chunks))
	seenKnowledge := map[string]struct{}{}
	for _, chunk := range chunks {
		id := strings.TrimSpace(chunk.KnowledgeID)
		if id == "" {
			continue
		}
		if _, exists := seenKnowledge[id]; exists {
			continue
		}
		seenKnowledge[id] = struct{}{}
		video := videoByID[chunk.VideoID]
		seconds := chunk.StartMs / 1000
		items = append(items, ChatEvidenceItem{
			KnowledgeID: id,
			VideoID:     chunk.VideoID,
			VideoTitle:  video.Title,
			VideoCover:  video.ThumbnailURL,
			Seconds:     seconds,
			Timestamp:   formatClock(seconds),
		})
	}
	c.JSON(http.StatusOK, gin.H{"data": items})
}

func formatClock(seconds int) string {
	if seconds < 0 {
		seconds = 0
	}
	return fmt.Sprintf("%02d:%02d", seconds/60, seconds%60)
}
