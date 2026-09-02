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
	"context"
	"encoding/json"
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

// WeKnora's persisted Wiki page_type enum does not include the product's
// case/methodology/insight types. Skill pages use page_type=index and carry
// the five-type business classification in frontmatter.primary_type (with
// frontmatter.type retained as a compatibility field).
const relatedKnowledgePageTypes = "entity,concept,index"

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
	fmType := frontmatterString(frontmatter, "primary_type")
	if fmType == "" {
		fmType = frontmatterString(frontmatter, "type")
	}
	if entitySubType == "" && knowledge.IsEntitySubType(fmType) {
		entitySubType = fmType
	}
	detail := wikiKnowledgeDetail(page.Content, knowledgeType, entitySubType)
	mergeStructuredFrontmatter(&detail, frontmatter, knowledgeType, entitySubType)
	timestampSource := detail.TimeRange
	if timestampSource == "" {
		timestampSource = page.Content
	}
	timestamp, seconds := wikiAnchorTimeline(timestampSource)
	return knowledge.AnchorItem{
		ID: page.ID, Slug: page.Slug,
		Title:                firstNonEmpty(frontmatterString(frontmatter, "title"), frontmatterString(frontmatter, "canonical_name"), firstMarkdownHeading(page.Content), page.Title, page.Slug),
		Type:                 knowledgeType,
		PrimaryType:          knowledgeType,
		KnowledgeObjectID:    frontmatterString(frontmatter, "knowledge_object_id"),
		TranscriptGeneration: frontmatterString(frontmatter, "transcript_generation"),
		AuditStatus:          frontmatterString(frontmatter, "audit_status"),
		CoreContent:          detail.CoreContent,
		StructureFields:      detail.StructureFields,
		EvidenceIDs:          detail.EvidenceIDs,
		InformationNature:    firstNonEmpty(detail.InformationNature, informationNatureLabel(knowledgeType, entitySubType)),
		TimeRange:            detail.TimeRange,
		RelatedContent:       detail.RelatedContent,
		Timestamp:            timestamp, Seconds: seconds, EntitySubType: entitySubType,
		PageType: page.PageType, Source: source,
	}
}

func knowledgeObjectType(frontmatter map[string]any) string {
	primaryType := strings.ToLower(frontmatterString(frontmatter, "primary_type"))
	compatibilityType := strings.ToLower(frontmatterString(frontmatter, "type"))
	if primaryType != "" && compatibilityType != "" && primaryType != compatibilityType {
		return ""
	}
	return firstNonEmpty(primaryType, compatibilityType)
}

func informationNatureLabel(knowledgeType knowledge.KnowledgeType, entitySubType string) string {
	if knowledgeType == knowledge.TypeEntity {
		return map[string]string{
			"person": "人物", "organization": "机构", "product": "产品",
			"technology": "技术", "industry": "行业", "place": "地点",
		}[entitySubType]
	}
	return map[knowledge.KnowledgeType]string{
		knowledge.TypeMethodology: "方法论",
		knowledge.TypeCase:        "案例",
		knowledge.TypeConcept:     "概念",
		knowledge.TypeInsight:     "洞察",
	}[knowledgeType]
}

type wikiKnowledgeDetailData struct {
	CoreContent       string
	StructureFields   []knowledge.DetailField
	EvidenceIDs       []string
	InformationNature string
	TimeRange         string
	Relations         []knowledge.StructuredRelation
	RelatedKnowledge  []knowledge.DetailLink
	RelatedEntities   []knowledge.DetailLink
	RelatedContent    []knowledge.DetailLink
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
	"methodology":  {{Key: "input", Label: "输入"}, {Key: "steps", Label: "步骤"}, {Key: "criteria", Label: "判断标准"}, {Key: "output", Label: "输出"}, {Key: "applicability", Label: "适用条件"}},
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
	if knowledgeType == knowledge.TypeMethodology {
		fieldSet = "methodology"
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
		TimeRange:         firstLabeledValue(values, "time_range"),
		Relations:         parseWikiStructuredRelations(firstLabeledValue(values, "relations")),
		RelatedKnowledge:  parseWikiDetailLinks(firstLabeledValue(values, "related_knowledge")),
		RelatedEntities:   parseWikiDetailLinks(firstLabeledValue(values, "related_entities")),
		RelatedContent:    parseWikiDetailLinks(firstLabeledValue(values, "related_content")),
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
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			label := strings.TrimSpace(strings.TrimLeft(line, "# "))
			if key := wikiDetailKey(label); key != "" {
				currentKey = key
				continue
			}
			currentKey = ""
			continue
		}
		if strings.HasPrefix(line, "|") {
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
	case "时间范围", "time_range":
		return "time_range"
	case "关联知识", "related_atom_ids", "related_knowledge":
		return "related_knowledge"
	case "关联实体", "related_entity_ids", "source_atom_ids", "related_entities":
		return "related_entities"
	case "相关内容", "related_content":
		return "related_content"
	case "结构化关系", "relations":
		return "relations"
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

func parseWikiDetailLinks(value string) []knowledge.DetailLink {
	links := make([]knowledge.DetailLink, 0)
	seen := map[string]struct{}{}
	add := func(title, slug string) {
		title = strings.TrimSpace(title)
		slug = strings.TrimSpace(slug)
		if title == "" && slug != "" {
			title = slug
		}
		if title == "" {
			return
		}
		key := strings.ToLower(slug + "\x00" + title)
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		links = append(links, knowledge.DetailLink{Title: title, Slug: slug})
	}

	for _, match := range regexp.MustCompile(`\[\[([^\]]+)\]\]`).FindAllStringSubmatch(value, -1) {
		target := strings.TrimSpace(match[1])
		if target == "" {
			continue
		}
		parts := strings.SplitN(target, "|", 2)
		slug := strings.TrimSpace(parts[0])
		title := slug
		if len(parts) == 2 && strings.TrimSpace(parts[1]) != "" {
			title = strings.TrimSpace(parts[1])
		}
		add(title, slug)
	}
	if len(links) > 0 {
		return links
	}

	cleaned := strings.NewReplacer("，", ",", "、", ",", "；", ",", ";", ",").Replace(value)
	for _, part := range strings.Split(cleaned, ",") {
		part = strings.TrimSpace(strings.Trim(part, "`[]()"))
		part = strings.TrimLeft(part, "-+* ")
		if part == "" || part == "无" || part == "暂无" {
			continue
		}
		add(part, "")
	}
	return links
}

func mergeDetailLinks(groups ...[]knowledge.DetailLink) []knowledge.DetailLink {
	out := make([]knowledge.DetailLink, 0)
	seen := map[string]struct{}{}
	for _, group := range groups {
		for _, link := range group {
			key := strings.ToLower(strings.TrimSpace(link.Slug) + "\x00" + strings.TrimSpace(link.Title))
			if key == "\x00" {
				continue
			}
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, link)
		}
	}
	return out
}

func frontmatterDetailLinks(raw any) []knowledge.DetailLink {
	items, ok := raw.([]any)
	if !ok {
		if value, ok := raw.(string); ok {
			return parseWikiDetailLinks(value)
		}
		return nil
	}
	out := make([]knowledge.DetailLink, 0, len(items))
	for _, item := range items {
		switch value := item.(type) {
		case string:
			out = append(out, parseWikiDetailLinks(value)...)
		case map[string]any:
			title := firstNonEmpty(frontmatterString(value, "title"), frontmatterString(value, "name"))
			slug := frontmatterString(value, "slug")
			if title == "" {
				title = slug
			}
			if title != "" {
				out = append(out, knowledge.DetailLink{Title: title, Slug: slug, TargetType: frontmatterString(value, "target_type")})
			}
		}
	}
	return mergeDetailLinks(out)
}

func parseWikiStructuredRelations(value string) []knowledge.StructuredRelation {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	var relations []knowledge.StructuredRelation
	if err := json.Unmarshal([]byte(value), &relations); err != nil {
		return nil
	}
	return relations
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
	// 关联知识只展示当前转写代次中通过 Skill 审计的知识对象；
	// 原生 Wiki 旧页面不再混入产品知识视图。
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
		fmType := knowledgeObjectType(frontmatter)
		knowledgeObjectID := frontmatterString(frontmatter, "knowledge_object_id")
		pageGeneration := frontmatterString(frontmatter, "transcript_generation")
		auditStatus := strings.ToLower(frontmatterString(frontmatter, "audit_status"))
		if knowledgeObjectID == "" ||
			pageGeneration != strings.TrimSpace(video.TranscriptGeneration) ||
			auditStatus != "passed" {
			continue
		}
		subType, _ := frontmatter["entity_sub_type"].(string)
		if subType == "" && knowledge.IsEntitySubType(fmType) {
			subType = fmType
		}
		mappedType := knowledge.MapPageTypeToKnowledgeType(p.PageType, fmType)
		if !knowledge.IsKnowledgeType(mappedType) {
			continue
		}
		anchor := wikiAnchor(p, mappedType, "skill")
		anchor.EntitySubType = subType
		anchor.SourceVideoTitle = video.Title
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
	PageType                 string            `json:"page_type"`    // outline / overview / summary / transcript_page
	ResultStage              string            `json:"result_stage"` // draft / final
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

type contentArtifactFailure struct {
	httpStatus int
	code       string
	message    string
	// staleGeneration marks a draft that belongs to a previous transcript
	// generation. It is a normal pipeline transition, not a corrupt artifact.
	staleGeneration bool
}

type contentArtifactCandidate struct {
	id    string
	stage string
}

// fetchWikiPageByVideoField 按 videos 表字段名取 Wiki 页
func (h *ContentHandler) fetchWikiPageByVideoField(c *gin.Context, video *model.Video, field string, pageType string) {
	candidates := make([]contentArtifactCandidate, 0, 2)
	switch field {
	case "outline_wiki_page_id":
		candidates = append(candidates,
			contentArtifactCandidate{id: video.OutlineWikiPageID, stage: "final"},
			contentArtifactCandidate{id: video.OutlineDraftWikiPageID, stage: "draft"},
		)
	case "overview_wiki_page_id":
		candidates = append(candidates, contentArtifactCandidate{id: video.OverviewWikiPageID, stage: "final"})
	case "summary_wiki_page_id":
		candidates = append(candidates,
			contentArtifactCandidate{id: video.SummaryWikiPageID, stage: "final"},
			contentArtifactCandidate{id: video.SummaryDraftWikiPageID, stage: "draft"},
		)
	case "transcript_page_wiki_page_id":
		candidates = append(candidates, contentArtifactCandidate{id: video.TranscriptPageWikiPageID, stage: "final"})
	}
	var lastFailure *contentArtifactFailure
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate.id) == "" {
			continue
		}
		response, failure := h.readWikiPageCandidate(c.Request.Context(), video, pageType, candidate)
		if failure == nil {
			c.JSON(http.StatusOK, response)
			return
		}
		if candidate.stage == "draft" && failure.staleGeneration {
			// A draft from the previous transcript generation can remain linked
			// while the current final artifact is still being generated. Do not
			// expose that expected transition as a contract error.
			continue
		}
		lastFailure = failure
	}
	if lastFailure == nil {
		contentError(c, http.StatusNotFound, video.ID, pageType, "not_generated", "wiki_page_id not yet generated", video.UpdatedAt)
		return
	}
	contentError(c, lastFailure.httpStatus, video.ID, pageType, lastFailure.code, lastFailure.message, video.UpdatedAt)
}

func (h *ContentHandler) readWikiPageCandidate(ctx context.Context, video *model.Video, pageType string, candidate contentArtifactCandidate) (*WikiPageResp, *contentArtifactFailure) {
	page, err := h.Wiki.GetPageByID(ctx, h.KBID, candidate.id)
	if err != nil {
		return nil, &contentArtifactFailure{httpStatus: http.StatusInternalServerError, code: "weknora_read_failed", message: err.Error()}
	}
	if page == nil {
		return nil, &contentArtifactFailure{httpStatus: http.StatusNotFound, code: "artifact_missing", message: "wiki page not found"}
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
	if generationMismatch && candidate.stage == "draft" {
		return nil, &contentArtifactFailure{
			httpStatus:      http.StatusNotFound,
			code:            "not_generated",
			message:         "wiki page is not ready for the current transcript generation",
			staleGeneration: true,
		}
	}
	if expectedType == "" || actualType != expectedType || sourceVideoID != video.ID || generationMismatch || strings.TrimSpace(page.Content) == "" {
		return nil, &contentArtifactFailure{httpStatus: http.StatusInternalServerError, code: "artifact_contract_mismatch", message: "wiki page does not satisfy the content artifact contract"}
	}
	var canonical outline.Document
	var summaryDocument *summary.Document
	responseContent := page.Content
	if pageType == "outline" {
		if document, parseErr := outline.Parse(page.Content); parseErr == nil {
			if pageSchemaVersion, ok := frontmatterInt(frontmatter, "schema_version"); !ok || pageSchemaVersion != outline.SchemaVersion {
				return nil, &contentArtifactFailure{httpStatus: http.StatusInternalServerError, code: "artifact_invalid", message: "outline page schema_version is unsupported"}
			}
			if validateErr := outline.Validate(document, video.DurationSeconds, nil); validateErr != nil {
				return nil, &contentArtifactFailure{httpStatus: http.StatusInternalServerError, code: "artifact_invalid", message: validateErr.Error()}
			}
			canonical = document
			responseContent = outline.RenderMarkdown(document)
		} else if !outline.IsLegacyMarkdown(page.Content) {
			return nil, &contentArtifactFailure{httpStatus: http.StatusInternalServerError, code: "artifact_invalid", message: "outline page is neither JSON Schema v1 nor valid legacy Markdown"}
		}
	}
	if pageType == "summary" {
		document, parseErr := summary.ParseStored(page.Content)
		if parseErr != nil {
			return nil, &contentArtifactFailure{httpStatus: http.StatusInternalServerError, code: "artifact_invalid", message: "summary page is not valid JSON"}
		}
		if validateErr := summary.ValidateStored(document, video.VideoType); validateErr != nil {
			return nil, &contentArtifactFailure{httpStatus: http.StatusInternalServerError, code: "artifact_invalid", message: validateErr.Error()}
		}
		summaryDocument = &document
		responseContent = ""
	}
	updatedAt := page.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = video.UpdatedAt
	}
	return &WikiPageResp{
		Status:                   "completed",
		Stage:                    pageType,
		ResultStage:              candidate.stage,
		UpdatedAt:                updatedAt,
		VideoID:                  video.ID,
		PageType:                 pageType,
		WikiPageID:               candidate.id,
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
	}, nil
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
