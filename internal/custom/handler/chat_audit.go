package handler

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Tencent/WeKnora/internal/custom/model"
)

type ChatAuditHandler struct {
	db *gorm.DB
}

type chatSourceAuditRequest struct {
	EventID            string   `json:"event_id"`
	SessionID          string   `json:"session_id"`
	Scope              string   `json:"scope"`
	VideoID            string   `json:"video_id"`
	SourceMode         string   `json:"source_mode"`
	FallbackUsed       bool     `json:"fallback_used"`
	ReferencesFound    int      `json:"references_found"`
	WikiPageIDs        []string `json:"wiki_page_ids"`
	KnowledgeObjectIDs []string `json:"knowledge_object_ids"`
	TranscriptChunkIDs []string `json:"transcript_chunk_ids"`
}

func NewChatAuditHandler(db *gorm.DB) *ChatAuditHandler {
	return &ChatAuditHandler{db: db}
}

// RecordSourceAudit stores only source identifiers returned by WeKnora. The
// endpoint is idempotent because the browser can retry after a completed turn.
func (h *ChatAuditHandler) RecordSourceAudit(c *gin.Context) {
	var input chatSourceAuditRequest
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "问答来源审计数据格式无效"})
		return
	}
	input.EventID = strings.TrimSpace(input.EventID)
	input.SessionID = strings.TrimSpace(input.SessionID)
	input.Scope = strings.TrimSpace(input.Scope)
	input.VideoID = strings.TrimSpace(input.VideoID)
	input.SourceMode = strings.TrimSpace(input.SourceMode)
	if input.EventID == "" || input.SessionID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "event_id 和 session_id 不能为空"})
		return
	}
	if input.ReferencesFound < 0 {
		input.ReferencesFound = 0
	}
	if input.SourceMode == "" {
		input.SourceMode = sourceMode(len(input.WikiPageIDs) > 0, len(input.TranscriptChunkIDs) > 0)
	}
	if input.SourceMode != "wiki" && input.SourceMode != "chunk" &&
		input.SourceMode != "wiki_and_chunk" && input.SourceMode != "none" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "source_mode 无效"})
		return
	}

	audit := model.ChatSourceAudit{
		ID:                 uuid.NewString(),
		EventID:            input.EventID,
		SessionID:          input.SessionID,
		Scope:              input.Scope,
		VideoID:            input.VideoID,
		SourceMode:         input.SourceMode,
		FallbackUsed:       input.FallbackUsed,
		ReferencesFound:    input.ReferencesFound,
		WikiPageIDs:        encodeIDs(input.WikiPageIDs),
		KnowledgeObjectIDs: encodeIDs(input.KnowledgeObjectIDs),
		TranscriptChunkIDs: encodeIDs(input.TranscriptChunkIDs),
	}
	result := h.db.WithContext(c.Request.Context()).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "event_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"session_id", "scope", "video_id", "source_mode", "fallback_used",
			"references_found", "wiki_page_ids", "knowledge_object_ids",
			"transcript_chunk_ids", "updated_at",
		}),
	}).Create(&audit)
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "保存问答来源审计失败: " + result.Error.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"event_id": input.EventID}})
}

func encodeIDs(values []string) string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	raw, _ := json.Marshal(out)
	return string(raw)
}

func sourceMode(hasWiki, hasChunk bool) string {
	switch {
	case hasWiki && hasChunk:
		return "wiki_and_chunk"
	case hasWiki:
		return "wiki"
	case hasChunk:
		return "chunk"
	default:
		return "none"
	}
}
