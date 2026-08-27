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
//   - 关联知识 Tab 走双源（原生 entity/concept + skill case/method/insight）
//   - 其他 Tab 走单源（对应 *_wiki_page_id 指向的页面）
package handler

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
	"github.com/Tencent/WeKnora/internal/custom/model"
	"github.com/Tencent/WeKnora/internal/custom/service/knowledge"
)

// ContentHandler 内容生产聚合 handler
type ContentHandler struct {
	DB   *gorm.DB
	Wiki *weknora.WikiClient
	KBID string
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

// RelatedKnowledge 关联知识 Tab（CP-T008）
func (h *ContentHandler) RelatedKnowledge(c *gin.Context) {
	video, ok := h.loadVideo(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()

	// 第一源：WeKnora 原生 entity + concept
	nativePages, err := h.Wiki.ListByVideo(ctx, h.KBID, video.ID, "entity")
	if err != nil {
		contentError(c, http.StatusInternalServerError, video.ID, "graph", "weknora_read_failed", "list native pages: "+err.Error(), video.UpdatedAt)
		return
	}
	conceptPages, err := h.Wiki.ListByVideo(ctx, h.KBID, video.ID, "concept")
	if err != nil {
		contentError(c, http.StatusInternalServerError, video.ID, "graph", "weknora_read_failed", "list concept pages: "+err.Error(), video.UpdatedAt)
		return
	}

	nativeAnchors := make([]knowledge.AnchorItem, 0, len(nativePages)+len(conceptPages))
	for _, p := range nativePages {
		fmType, _ := p.ParsedFrontmatter()["type"].(string)
		subType, _ := p.ParsedFrontmatter()["entity_sub_type"].(string)
		nativeAnchors = append(nativeAnchors, knowledge.AnchorItem{
			ID:            p.ID,
			Slug:          p.Slug,
			Title:         p.Title,
			Type:          knowledge.MapPageTypeToKnowledgeType(p.PageType, fmType),
			EntitySubType: subType,
			PageType:      p.PageType,
			Source:        "native",
		})
	}
	for _, p := range conceptPages {
		fmType, _ := p.ParsedFrontmatter()["type"].(string)
		nativeAnchors = append(nativeAnchors, knowledge.AnchorItem{
			ID:       p.ID,
			Slug:     p.Slug,
			Title:    p.Title,
			Type:     knowledge.MapPageTypeToKnowledgeType(p.PageType, fmType),
			PageType: p.PageType,
			Source:   "native",
		})
	}

	// 第二源：skill 产物（page_type=index，含 case/method/insight + entity 6 类细分）
	skillPages, err := h.Wiki.ListByVideo(ctx, h.KBID, video.ID, "index")
	if err != nil {
		contentError(c, http.StatusInternalServerError, video.ID, "graph", "weknora_read_failed", "list skill pages: "+err.Error(), video.UpdatedAt)
		return
	}
	skillAnchors := make([]knowledge.AnchorItem, 0, len(skillPages))
	for _, p := range skillPages {
		fmType, _ := p.ParsedFrontmatter()["type"].(string)
		// 跳过 4 类知识原子页面（methodology/case/insight/concept）和实体 6 类细分页
		// 不包括「知识底座索引页」自身（type=knowledge_base）
		if fmType == "knowledge_base" || fmType == "outline" || fmType == "overview" ||
			fmType == "typed_summary" || fmType == "transcript_page" {
			continue
		}
		subType, _ := p.ParsedFrontmatter()["entity_sub_type"].(string)
		skillAnchors = append(skillAnchors, knowledge.AnchorItem{
			ID:            p.ID,
			Slug:          p.Slug,
			Title:         p.Title,
			Type:          knowledge.MapSkillToKnowledgeType(fmType),
			EntitySubType: subType,
			PageType:      p.PageType,
			Source:        "skill",
		})
	}

	merged := knowledge.MergeAnchors(nativeAnchors, skillAnchors)

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
	Status       string         `json:"status"`
	Stage        string         `json:"stage"`
	ErrorCode    string         `json:"error_code"`
	ErrorMessage string         `json:"error_message"`
	UpdatedAt    time.Time      `json:"updated_at"`
	VideoID      string         `json:"video_id"`
	PageType     string         `json:"page_type"` // outline / overview / summary / transcript_page
	WikiPageID   string         `json:"wiki_page_id"`
	Content      string         `json:"content"`
	Frontmatter  map[string]any `json:"frontmatter,omitempty"`
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
	updatedAt := page.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = video.UpdatedAt
	}
	c.JSON(http.StatusOK, WikiPageResp{
		Status:      "completed",
		Stage:       pageType,
		UpdatedAt:   updatedAt,
		VideoID:     video.ID,
		PageType:    pageType,
		WikiPageID:  wikiID,
		Content:     page.Content,
		Frontmatter: page.ParsedFrontmatter(),
	})
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
