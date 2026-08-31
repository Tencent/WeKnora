// Package handler 内容生产聚合 API（CP-T008 + CP-T009）。
//
// 端点：
//   - GET /api/custom/videos/:id/related-knowledge   关联知识（5 类型双源合并）
//   - GET /api/custom/videos/:id/outline             章节大纲
//   - GET /api/custom/videos/:id/overview            快速概要
//   - GET /api/custom/videos/:id/summary             智能总结
//   - GET /api/custom/videos/:id/transcript-page     完整文字稿页面
//
// 设计要点：
//   - 数据源均在 WeKnora Wiki，后端代理 + 字段映射
//   - 关联知识 Tab 读取五类知识页面，兼容原生页面与 extract-video-knowledge 产物
//   - 其他 Tab 走单源（对应 *_wiki_page_id 指向的页面）
package handler

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
	"github.com/Tencent/WeKnora/internal/custom/model"
	"github.com/Tencent/WeKnora/internal/custom/service/knowledge"
	"github.com/Tencent/WeKnora/internal/custom/service/outline"
	"github.com/Tencent/WeKnora/internal/custom/service/summary"
)

// ContentHandler 内容生产聚合 handler
type ContentHandler struct {
	DB   *gorm.DB
	Wiki *weknora.WikiClient
	KBID string
}

var wikiTimestampPattern = regexp.MustCompile(`\b(\d{1,3}:\d{2}(?::\d{2})?)\b`)

const relatedKnowledgePageTypes = "entity,concept,case,methodology,insight,index"

func wikiAnchorTimeline(content string) (string, int) {
	match := wikiTimestampPattern.FindStringSubmatch(content)
	if len(match) != 2 {
		return "", 0
	}
	parts := strings.Split(match[1], ":")
	values := make([]int, len(parts))
	for index, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 {
			return "", 0
		}
		values[index] = value
	}
	if values[len(values)-1] >= 60 || (len(values) == 3 && values[1] >= 60) {
		return "", 0
	}
	if len(values) == 2 {
		return match[1], values[0]*60 + values[1]
	}
	return match[1], values[0]*3600 + values[1]*60 + values[2]
}

func wikiAnchor(page weknora.WikiPage, knowledgeType knowledge.KnowledgeType, source string) knowledge.AnchorItem {
	frontmatter := page.ParsedFrontmatter()
	entitySubType, _ := frontmatter["entity_sub_type"].(string)
	timestamp, seconds := wikiAnchorTimeline(page.Content)
	return knowledge.AnchorItem{
		ID: page.ID, Slug: page.Slug, Title: page.Title, Type: knowledgeType,
		Timestamp: timestamp, Seconds: seconds, EntitySubType: entitySubType,
		PageType: page.PageType, Source: source,
	}
}

// NewContentHandler 构造
func NewContentHandler(db *gorm.DB, wiki *weknora.WikiClient, kbID string) *ContentHandler {
	return &ContentHandler{DB: db, Wiki: wiki, KBID: kbID}
}

// loadVideo 从 DB 取 video，404 直接终止
func (h *ContentHandler) loadVideo(c *gin.Context) (*model.Video, bool) {
	id := c.Param("id")
	var v model.Video
	if err := h.DB.First(&v, "id = ?", id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "video not found"})
		return nil, false
	}
	return &v, true
}

// RelatedKnowledgeResp 关联知识聚合响应（CP-T008）
type RelatedKnowledgeResp struct {
	Status       string                                             `json:"status"`
	Stage        string                                             `json:"stage"`
	ErrorCode    string                                             `json:"error_code"`
	ErrorMessage string                                             `json:"error_message"`
	UpdatedAt    time.Time                                          `json:"updated_at"`
	VideoID      string                                             `json:"video_id"`
	KBID         string                                             `json:"kb_id"`
	Anchors      map[knowledge.KnowledgeType][]knowledge.AnchorItem `json:"anchors"`     // 5 类型分组
	CrossVideo   []knowledge.AnchorItem                             `json:"cross_video"` // 跨视频边（CP-T008 后续接 Neo4j）
}

func isKnowledgeBaseWikiPage(page *weknora.WikiPage, videoID string) bool {
	if page == nil || page.PageType != "index" || strings.TrimSpace(page.Content) == "" {
		return false
	}
	frontmatter := page.ParsedFrontmatter()
	pageType, _ := frontmatter["type"].(string)
	sourceVideoID, _ := frontmatter["source_video_id"].(string)
	if pageType == "knowledge_base" && sourceVideoID == videoID {
		return true
	}
	return len(frontmatter) == 0 &&
		page.Slug == "video/"+videoID &&
		strings.Contains(page.Content, videoID)
}

func (h *ContentHandler) requireKnowledgeBase(c *gin.Context, video *model.Video) (*weknora.WikiPage, bool) {
	if strings.TrimSpace(video.KnowledgeBaseWikiPageID) == "" {
		contentError(c, http.StatusNotFound, video.ID, "graph", "not_generated", "knowledge_base wiki page not yet generated", video.UpdatedAt)
		return nil, false
	}
	page, err := h.Wiki.GetPageByID(c.Request.Context(), h.KBID, video.KnowledgeBaseWikiPageID)
	if err != nil {
		contentError(c, http.StatusInternalServerError, video.ID, "graph", "weknora_read_failed", "read knowledge_base wiki page: "+err.Error(), video.UpdatedAt)
		return nil, false
	}
	if !isKnowledgeBaseWikiPage(page, video.ID) {
		contentError(c, http.StatusNotFound, video.ID, "graph", "artifact_missing", "knowledge_base wiki page is invalid or belongs to another video", video.UpdatedAt)
		return nil, false
	}
	return page, true
}

// RelatedKnowledge 关联知识 Tab（CP-T008）
func (h *ContentHandler) RelatedKnowledge(c *gin.Context) {
	video, ok := h.loadVideo(c)
	if !ok {
		return
	}
	knowledgeBasePage, ok := h.requireKnowledgeBase(c, video)
	if !ok {
		return
	}
	ctx := c.Request.Context()

	// 一次读取当前知识底座涉及的全部页面，再映射到五类知识。
	pages, err := h.Wiki.ListByVideoOwned(ctx, h.KBID, video.ID, relatedKnowledgePageTypes, knowledgeBasePage)
	if err != nil {
		contentError(c, http.StatusInternalServerError, video.ID, "graph", "weknora_read_failed", "list knowledge pages: "+err.Error(), video.UpdatedAt)
		return
	}

	anchors := make([]knowledge.AnchorItem, 0, len(pages))
	for _, p := range pages {
		if p.ID == knowledgeBasePage.ID {
			continue
		}
		frontmatter := p.ParsedFrontmatter()
		fmType, _ := frontmatter["type"].(string)
		subType, _ := frontmatter["entity_sub_type"].(string)
		if subType == "" && knowledge.IsEntitySubType(fmType) {
			subType = fmType
		}
		mappedType := knowledge.MapPageTypeToKnowledgeType(p.PageType, fmType)
		if !knowledge.IsKnowledgeType(mappedType) {
			continue
		}
		source := "skill"
		if p.PageType == "entity" || p.PageType == "concept" {
			source = "native"
		}
		anchor := wikiAnchor(p, mappedType, source)
		anchor.EntitySubType = subType
		anchors = append(anchors, anchor)
	}

	merged := knowledge.MergeAnchors(anchors, nil)

	// 跨视频边（CP-T008 后续接 Neo4j；本版本返回空）
	c.JSON(http.StatusOK, RelatedKnowledgeResp{
		Status:     "completed",
		Stage:      "graph",
		UpdatedAt:  video.UpdatedAt,
		VideoID:    video.ID,
		KBID:       h.KBID,
		Anchors:    merged,
		CrossVideo: []knowledge.AnchorItem{},
	})
}

// WikiPageResp 单页 Wiki 响应（CP-T009）
type WikiPageResp struct {
	Status                   string            `json:"status"`
	Stage                    string            `json:"stage"`
	ErrorCode                string            `json:"error_code"`
	ErrorMessage             string            `json:"error_message"`
	UpdatedAt                time.Time         `json:"updated_at"`
	VideoID                  string            `json:"video_id"`
	PageType                 string            `json:"page_type"` // outline / overview / summary / transcript_page
	WikiPageID               string            `json:"wiki_page_id"`
	TranscriptGeneration     string            `json:"transcript_generation"`
	ArtifactVersion          int               `json:"artifact_version"`
	SchemaVersion            int               `json:"schema_version,omitempty"`
	Chapters                 []outline.Chapter `json:"chapters,omitempty"`
	SummarySource            string            `json:"summary_source,omitempty"`
	SummaryKnowledgeEnhanced bool              `json:"summary_knowledge_enhanced,omitempty"`
	SummaryUserEdited        bool              `json:"summary_user_edited,omitempty"`
	KnowledgeAuditStatus     string            `json:"knowledge_audit_status,omitempty"`
	Summary                  *summary.Document `json:"summary,omitempty"`
	Content                  string            `json:"content"`
	Frontmatter              map[string]any    `json:"frontmatter,omitempty"`
}

// fetchWikiPageByVideoField 按 videos 表字段名取 Wiki 页
func (h *ContentHandler) fetchWikiPageByVideoField(c *gin.Context, video *model.Video, field string, pageType string) {
	wikiID := ""
	switch field {
	case "outline_wiki_page_id":
		wikiID = video.OutlineWikiPageID
	case "overview_wiki_page_id":
		wikiID = video.OverviewWikiPageID
	case "summary_wiki_page_id":
		wikiID = video.SummaryWikiPageID
	case "transcript_page_wiki_page_id":
		wikiID = video.TranscriptPageWikiPageID
	}
	if wikiID == "" {
		contentError(c, http.StatusNotFound, video.ID, pageType, "not_generated", "wiki_page_id not yet generated", video.UpdatedAt)
		return
	}
	page, err := h.Wiki.GetPageByID(c.Request.Context(), h.KBID, wikiID)
	if err != nil {
		contentError(c, http.StatusInternalServerError, video.ID, pageType, "weknora_read_failed", err.Error(), video.UpdatedAt)
		return
	}
	if page == nil {
		contentError(c, http.StatusNotFound, video.ID, pageType, "artifact_missing", "wiki page not found", video.UpdatedAt)
		return
	}
	frontmatter := page.ParsedFrontmatter()
	expectedType := map[string]string{
		"outline":         "outline",
		"overview":        "overview",
		"summary":         "typed_summary",
		"transcript_page": "transcript_page",
	}[pageType]
	actualType, _ := frontmatter["type"].(string)
	sourceVideoID, _ := frontmatter["source_video_id"].(string)
	pageGeneration, _ := frontmatter["transcript_generation"].(string)
	generationMismatch := strings.TrimSpace(video.TranscriptGeneration) != "" && strings.TrimSpace(pageGeneration) != video.TranscriptGeneration
	if expectedType == "" || actualType != expectedType || sourceVideoID != video.ID || generationMismatch || strings.TrimSpace(page.Content) == "" {
		contentError(c, http.StatusInternalServerError, video.ID, pageType, "artifact_contract_mismatch", "wiki page does not satisfy the content artifact contract", video.UpdatedAt)
		return
	}
	var canonical outline.Document
	var summaryDocument *summary.Document
	responseContent := page.Content
	if pageType == "outline" {
		if document, parseErr := outline.Parse(page.Content); parseErr == nil {
			if pageSchemaVersion, ok := frontmatterInt(frontmatter, "schema_version"); !ok || pageSchemaVersion != outline.SchemaVersion {
				contentError(c, http.StatusInternalServerError, video.ID, pageType, "artifact_invalid", "outline page schema_version is unsupported", video.UpdatedAt)
				return
			}
			if validateErr := outline.Validate(document, video.DurationSeconds, nil); validateErr != nil {
				contentError(c, http.StatusInternalServerError, video.ID, pageType, "artifact_invalid", validateErr.Error(), video.UpdatedAt)
				return
			}
			canonical = document
			responseContent = outline.RenderMarkdown(document)
		} else if !outline.IsLegacyMarkdown(page.Content) {
			contentError(c, http.StatusInternalServerError, video.ID, pageType, "artifact_invalid", "outline page is neither JSON Schema v1 nor valid legacy Markdown", video.UpdatedAt)
			return
		}
	}
	if pageType == "summary" {
		document, parseErr := summary.ParseStored(page.Content)
		if parseErr != nil {
			contentError(c, http.StatusInternalServerError, video.ID, pageType, "artifact_invalid", "summary page is not valid JSON", video.UpdatedAt)
			return
		}
		if validateErr := summary.ValidateStored(document, video.VideoType); validateErr != nil {
			contentError(c, http.StatusInternalServerError, video.ID, pageType, "artifact_invalid", validateErr.Error(), video.UpdatedAt)
			return
		}
		summaryDocument = &document
		responseContent = ""
	}
	updatedAt := page.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = video.UpdatedAt
	}
	c.JSON(http.StatusOK, WikiPageResp{
		Status:                   "completed",
		Stage:                    pageType,
		UpdatedAt:                updatedAt,
		VideoID:                  video.ID,
		PageType:                 pageType,
		WikiPageID:               wikiID,
		TranscriptGeneration:     video.TranscriptGeneration,
		ArtifactVersion:          page.Version,
		SchemaVersion:            canonical.SchemaVersion,
		Chapters:                 canonical.Chapters,
		SummarySource:            video.SummarySource,
		SummaryKnowledgeEnhanced: video.SummaryKnowledgeEnhanced,
		SummaryUserEdited:        video.SummaryUserEdited,
		KnowledgeAuditStatus:     video.KnowledgeAuditStatus,
		Summary:                  summaryDocument,
		Content:                  responseContent,
		Frontmatter:              frontmatter,
	})
}

func frontmatterInt(frontmatter map[string]any, key string) (int, bool) {
	value, ok := frontmatter[key]
	if !ok {
		return 0, false
	}
	switch number := value.(type) {
	case int:
		return number, true
	case int8:
		return int(number), true
	case int16:
		return int(number), true
	case int32:
		return int(number), true
	case int64:
		return int(number), true
	case uint:
		return int(number), true
	case uint8:
		return int(number), true
	case uint16:
		return int(number), true
	case uint32:
		return int(number), true
	case uint64:
		return int(number), true
	case float64:
		return int(number), number == float64(int(number))
	default:
		return 0, false
	}
}

func contentError(c *gin.Context, httpStatus int, videoID, stage, code, message string, updatedAt time.Time) {
	c.JSON(httpStatus, gin.H{
		"status": "failed", "stage": stage, "error_code": code, "error_message": message,
		"updated_at": updatedAt, "video_id": videoID, "error": message,
	})
}

// Outline 章节大纲（CP-T009）
func (h *ContentHandler) Outline(c *gin.Context) {
	v, ok := h.loadVideo(c)
	if !ok {
		return
	}
	h.fetchWikiPageByVideoField(c, v, "outline_wiki_page_id", "outline")
}

// Overview 快速概要（CP-T009）
func (h *ContentHandler) Overview(c *gin.Context) {
	v, ok := h.loadVideo(c)
	if !ok {
		return
	}
	h.fetchWikiPageByVideoField(c, v, "overview_wiki_page_id", "overview")
}

// Summary 智能总结（CP-T009）
func (h *ContentHandler) Summary(c *gin.Context) {
	v, ok := h.loadVideo(c)
	if !ok {
		return
	}
	h.fetchWikiPageByVideoField(c, v, "summary_wiki_page_id", "summary")
}

// TranscriptPage 完整文字稿页面（CP-T009）
func (h *ContentHandler) TranscriptPage(c *gin.Context) {
	v, ok := h.loadVideo(c)
	if !ok {
		return
	}
	h.fetchWikiPageByVideoField(c, v, "transcript_page_wiki_page_id", "transcript_page")
}
