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
	fmType, _ := frontmatter["type"].(string)
	if entitySubType == "" && knowledge.IsEntitySubType(fmType) {
		entitySubType = fmType
	}
	timestamp, seconds := wikiAnchorTimeline(page.Content)
	detail := wikiKnowledgeDetail(page.Content, knowledgeType, entitySubType)
	return knowledge.AnchorItem{
		ID: page.ID, Slug: page.Slug, Title: page.Title, Type: knowledgeType,
		CoreContent:       detail.CoreContent,
		StructureFields:   detail.StructureFields,
		EvidenceIDs:       detail.EvidenceIDs,
		InformationNature: detail.InformationNature,
		Timestamp:         timestamp, Seconds: seconds, EntitySubType: entitySubType,
		PageType: page.PageType, Source: source,
	}
}

type wikiKnowledgeDetailData struct {
	CoreContent       string
	StructureFields   []knowledge.DetailField
	EvidenceIDs       []string
	InformationNature string
}

type frameworkField struct {
	Key   string
	Label string
}

var typeFrameworkFields = map[string][]frameworkField{
	"person":       {{Key: "identity", Label: "职业身份"}, {Key: "background", Label: "教育背景与经历"}, {Key: "expertise", Label: "擅长领域"}, {Key: "standpoint", Label: "代表性观点"}},
	"organization": {{Key: "org_type", Label: "机构类型"}, {Key: "industry", Label: "所在行业"}, {Key: "stage", Label: "发展阶段"}, {Key: "core_business", Label: "核心业务"}, {Key: "key_people", Label: "关键人物"}},
	"product":      {{Key: "product_type", Label: "产品类别"}, {Key: "target_users", Label: "目标用户"}, {Key: "core_function", Label: "核心功能"}, {Key: "tech_basis", Label: "技术基础"}, {Key: "differentiation", Label: "差异化特点"}},
	"technology":   {{Key: "tech_category", Label: "技术分类"}, {Key: "application_area", Label: "应用领域"}, {Key: "maturity", Label: "发展阶段"}},
	"industry":     {{Key: "scope", Label: "行业范围"}, {Key: "stage", Label: "发展阶段"}, {Key: "key_trends", Label: "关键趋势"}},
	"place":        {{Key: "place_type", Label: "地点类型"}, {Key: "associated_activity", Label: "关联活动"}},
	"method":       {{Key: "input", Label: "输入"}, {Key: "steps", Label: "步骤"}, {Key: "criteria", Label: "判断标准"}, {Key: "output", Label: "输出"}, {Key: "applicability", Label: "适用条件"}},
	"case":         {{Key: "context", Label: "背景"}, {Key: "actors", Label: "参与对象"}, {Key: "choices", Label: "选择"}, {Key: "actions", Label: "行动"}, {Key: "outcome", Label: "结果"}, {Key: "retrospective", Label: "复盘判断"}},
	"concept":      {{Key: "definition", Label: "定义"}, {Key: "components", Label: "构成要素"}, {Key: "mechanism", Label: "运行机制"}, {Key: "distinction", Label: "相邻区别"}},
	"insight":      {{Key: "claim", Label: "核心判断"}, {Key: "reasoning", Label: "推导依据"}, {Key: "qualifications", Label: "限定条件"}, {Key: "implications", Label: "影响建议"}},
}

var structureFieldAliases = map[string]string{
	"职业身份": "identity", "身份": "identity",
	"教育背景": "background", "教育背景与经历": "background", "关键职业经历和转折点": "background",
	"擅长领域": "expertise", "关注方向": "expertise",
	"代表性观点": "standpoint", "判断倾向": "standpoint",
	"机构类型": "org_type",
	"所在行业": "industry", "行业": "industry",
	"发展阶段": "stage", "规模": "stage",
	"核心业务": "core_business", "代表性项目": "core_business",
	"关键人物": "key_people",
	"产品类别": "product_type", "产品类型": "product_type",
	"目标用户":  "target_users",
	"核心功能":  "core_function",
	"技术基础":  "tech_basis",
	"差异化特点": "differentiation", "竞争定位": "differentiation",
	"技术分类": "tech_category",
	"应用领域": "application_area",
	"成熟度":  "maturity",
	"行业范围": "scope", "范围": "scope",
	"关键趋势": "key_trends",
	"地点类型": "place_type",
	"关联活动": "associated_activity", "关联活动或事件": "associated_activity",
	"输入": "input", "前提": "input",
	"步骤": "steps", "操作步骤": "steps", "行动序列": "steps",
	"判断标准": "criteria", "标准": "criteria", "取舍依据": "criteria",
	"输出": "output", "产出": "output",
	"适用条件": "applicability", "限制": "applicability",
	"背景": "context", "具体情境": "context", "情境": "context",
	"参与对象": "actors", "参与者": "actors",
	"选择": "choices", "选项": "choices", "面临选项": "choices",
	"行动": "actions", "实际执行": "actions", "关键动作": "actions",
	"结果": "outcome", "后续影响": "outcome",
	"复盘判断": "retrospective", "事后复盘": "retrospective",
	"定义": "definition", "核心界定": "definition",
	"构成要素": "components", "内部结构": "components",
	"运行机制": "mechanism", "原理": "mechanism",
	"相邻区别": "distinction", "关键区别": "distinction",
	"核心判断": "claim", "主张": "claim",
	"推导依据": "reasoning", "推导过程": "reasoning", "依据": "reasoning",
	"限定条件": "qualifications", "适用范围": "qualifications",
	"影响建议": "implications", "推论": "implications", "影响": "implications", "行动建议": "implications",
}

func wikiKnowledgeDetail(content string, knowledgeType knowledge.KnowledgeType, entitySubType string) wikiKnowledgeDetailData {
	body := stripWikiFrontmatter(content)
	fieldSet := string(knowledgeType)
	if knowledgeType == knowledge.TypeMethod {
		fieldSet = "method"
	}
	if knowledgeType == knowledge.TypeEntity && entitySubType != "" {
		fieldSet = entitySubType
	}
	values := parseLabeledValues(body)
	fields := make([]knowledge.DetailField, 0)
	for _, field := range typeFrameworkFields[fieldSet] {
		if value := strings.TrimSpace(values[field.Key]); value != "" {
			fields = append(fields, knowledge.DetailField{Key: field.Key, Label: field.Label, Value: value})
		}
	}
	return wikiKnowledgeDetailData{
		CoreContent:       firstLabeledValue(values, "core_content", "一句话概述", "description"),
		StructureFields:   fields,
		EvidenceIDs:       splitEvidenceIDs(firstLabeledValue(values, "evidence_ids")),
		InformationNature: firstLabeledValue(values, "information_nature"),
	}
}

func stripWikiFrontmatter(content string) string {
	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, "---") {
		return trimmed
	}
	lines := strings.Split(trimmed, "\n")
	for index := 1; index < len(lines); index++ {
		if strings.TrimSpace(lines[index]) == "---" {
			return strings.TrimSpace(strings.Join(lines[index+1:], "\n"))
		}
	}
	return trimmed
}

func parseLabeledValues(content string) map[string]string {
	values := map[string]string{}
	var currentKey string
	for _, rawLine := range strings.Split(content, "\n") {
		line := normalizeWikiDetailLine(rawLine)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "|") {
			if label, value, ok := splitWikiTableRow(rawLine); ok {
				if key := wikiDetailKey(label); key != "" {
					values[key] = appendWikiDetailValue(values[key], value)
					currentKey = key
					continue
				}
			}
			currentKey = ""
			continue
		}
		label, value, ok := splitWikiLabelValue(line)
		if ok {
			if key := wikiDetailKey(label); key != "" {
				values[key] = appendWikiDetailValue(values[key], value)
				currentKey = key
				continue
			}
		}
		if currentKey != "" && !strings.HasPrefix(line, "-") {
			values[currentKey] = appendWikiDetailValue(values[currentKey], line)
		}
	}
	return values
}

func normalizeWikiDetailLine(line string) string {
	line = strings.TrimSpace(line)
	line = strings.TrimLeft(line, "-+* ")
	line = strings.TrimSpace(line)
	line = strings.Trim(line, "` ")
	line = strings.ReplaceAll(line, "**", "")
	return strings.TrimSpace(line)
}

func splitWikiLabelValue(line string) (string, string, bool) {
	for _, sep := range []string{"：", ":"} {
		parts := strings.SplitN(line, sep, 2)
		if len(parts) == 2 {
			return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), true
		}
	}
	return "", "", false
}

func splitWikiTableRow(line string) (string, string, bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "|") || !strings.HasSuffix(line, "|") {
		return "", "", false
	}
	line = strings.Trim(line, "|")
	parts := strings.Split(line, "|")
	if len(parts) < 2 {
		return "", "", false
	}
	label := normalizeWikiDetailLine(parts[0])
	value := normalizeWikiDetailLine(strings.Join(parts[1:], "|"))
	if label == "" || value == "" || tableDivider(label) || tableDivider(value) || label == "字段" || label == "内容" || label == "含义" || label == "示例" {
		return "", "", false
	}
	return label, value, true
}

func tableDivider(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char != '-' && char != ':' && char != ' ' {
			return false
		}
	}
	return true
}

func wikiDetailKey(label string) string {
	label = strings.TrimSpace(strings.Trim(label, "` *"))
	switch label {
	case "核心内容", "core_content":
		return "core_content"
	case "一句话概述", "description":
		return "description"
	case "证据 ID", "证据ID", "evidence_ids", "evidence IDs", "evidence ids":
		return "evidence_ids"
	case "信息性质", "information_nature":
		return "information_nature"
	}
	return structureFieldAliases[label]
}

func appendWikiDetailValue(current, next string) string {
	next = strings.TrimSpace(next)
	if next == "" {
		return current
	}
	if current == "" {
		return next
	}
	return current + "\n" + next
}

func firstLabeledValue(values map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(values[key]); value != "" {
			return value
		}
	}
	return ""
}

func splitEvidenceIDs(value string) []string {
	value = strings.NewReplacer("，", ",", "、", ",", ";", ",", "；", ",", " ", ",").Replace(value)
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, part := range parts {
		part = strings.TrimSpace(strings.Trim(part, "`[]()"))
		if part == "" {
			continue
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		out = append(out, part)
	}
	return out
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
