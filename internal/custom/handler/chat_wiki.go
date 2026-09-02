package handler

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
	"github.com/Tencent/WeKnora/internal/custom/model"
	"github.com/Tencent/WeKnora/internal/custom/service/knowledge"
)

type ChatWikiHandler struct {
	db   *gorm.DB
	wiki *weknora.WikiClient
	kbID string
}

type ChatWikiSearchResult struct {
	WikiPageID               string                  `json:"wiki_page_id"`
	KnowledgeObjectID        string                  `json:"knowledge_object_id"`
	KnowledgeType            knowledge.KnowledgeType `json:"knowledge_type"`
	Title                    string                  `json:"title"`
	Summary                  string                  `json:"summary,omitempty"`
	Content                  string                  `json:"content"`
	SourceVideoID            string                  `json:"source_video_id"`
	TranscriptGeneration     string                  `json:"transcript_generation"`
	EvidenceIDs              []string                `json:"evidence_ids"`
	ClassificationConfidence float64                 `json:"classification_confidence"`
}

func NewChatWikiHandler(db *gorm.DB, wiki *weknora.WikiClient, kbID string) *ChatWikiHandler {
	return &ChatWikiHandler{db: db, wiki: wiki, kbID: strings.TrimSpace(kbID)}
}

// Search returns only passed Wiki knowledge objects. It is a direct product
// retrieval entry point; the agent remains responsible for answer generation.
func (h *ChatWikiHandler) Search(c *gin.Context) {
	query := strings.TrimSpace(c.Query("q"))
	if query == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "q 不能为空"})
		return
	}
	if h.wiki == nil || h.kbID == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "error": "Wiki knowledge base is unavailable"})
		return
	}
	limit := 20
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		if _, err := fmt.Sscanf(raw, "%d", &limit); err != nil || limit <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "limit must be a positive integer"})
			return
		}
	}
	if limit > 50 {
		limit = 50
	}
	expectedVideoID, expectedGeneration := "", ""
	if videoID := strings.TrimSpace(c.Query("video_id")); videoID != "" {
		var video model.Video
		if err := h.db.WithContext(c.Request.Context()).First(&video, "id = ?", videoID).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"success": false, "error": "video not found"})
			return
		}
		expectedVideoID = video.ID
		expectedGeneration = strings.TrimSpace(video.TranscriptGeneration)
	}
	pages, err := h.wiki.ListAllPages(c.Request.Context(), h.kbID, "index")
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"success": false, "error": err.Error()})
		return
	}
	types, err := parseGraphTypes(c.Query("type"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": err.Error()})
		return
	}
	type scored struct {
		item  ChatWikiSearchResult
		score int
	}
	results := make([]scored, 0)
	for _, page := range pages {
		validation, validationErr := knowledge.ValidateWikiObjectPage(page.Content, page.PageType, expectedVideoID, expectedGeneration)
		if validationErr != nil {
			continue
		}
		if len(types) > 0 && !containsKnowledgeType(types, validation.KnowledgeType) {
			continue
		}
		score := wikiSearchScore(query, page, validation.Title, validation.StructureFields)
		if score == 0 {
			continue
		}
		summary := firstNonEmpty(
			frontmatterString(page.ParsedFrontmatter(), "summary"),
			frontmatterString(page.ParsedFrontmatter(), "core_content"),
			frontmatterString(page.ParsedFrontmatter(), "description"),
		)
		results = append(results, scored{item: ChatWikiSearchResult{
			WikiPageID: page.ID, KnowledgeObjectID: validation.KnowledgeObjectID,
			KnowledgeType: validation.KnowledgeType, Title: validation.Title, Summary: summary,
			Content: stripWikiFrontmatter(page.Content), SourceVideoID: validation.SourceVideoID,
			TranscriptGeneration: validation.TranscriptGeneration, EvidenceIDs: validation.EvidenceIDs,
			ClassificationConfidence: validation.ClassificationConfidence,
		}, score: score})
	}
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].score == results[j].score {
			return results[i].item.Title < results[j].item.Title
		}
		return results[i].score > results[j].score
	})
	if len(results) > limit {
		results = results[:limit]
	}
	out := make([]ChatWikiSearchResult, 0, len(results))
	for _, result := range results {
		out = append(out, result.item)
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": out})
}

func wikiSearchScore(query string, page weknora.WikiPage, title string, fields map[string]string) int {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return 0
	}
	titleText := strings.ToLower(title)
	contentText := strings.ToLower(page.Content)
	score := 0
	if strings.Contains(titleText, query) {
		score += 8
	}
	if strings.Contains(contentText, query) {
		score += 3
	}
	for _, field := range fields {
		if strings.Contains(strings.ToLower(field), query) {
			score += 4
		}
	}
	if score > 0 {
		return score
	}
	for _, token := range strings.Fields(query) {
		if token != "" && strings.Contains(contentText, token) {
			score++
		}
	}
	return score
}

func containsKnowledgeType(types []knowledge.KnowledgeType, target knowledge.KnowledgeType) bool {
	for _, item := range types {
		if item == target {
			return true
		}
	}
	return false
}
