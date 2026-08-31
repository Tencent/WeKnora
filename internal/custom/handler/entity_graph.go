package handler

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
	"github.com/Tencent/WeKnora/internal/custom/model"
	"github.com/Tencent/WeKnora/internal/custom/service/knowledge"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type graphSource interface {
	GetEntityGraph(context.Context, string, int, []string) (*weknora.EntityGraphData, error)
}

type EntityGraphEvidence struct {
	VideoID    string   `json:"video_id"`
	VideoTitle string   `json:"video_title"`
	StartMs    int      `json:"start_ms"`
	EndMs      int      `json:"end_ms"`
	ChunkIndex int      `json:"chunk_index"`
	ChunkIDs   []string `json:"chunk_ids,omitempty"`
}

type EntityGraphKnowledgeDetail struct {
	ID                string                  `json:"id"`
	Slug              string                  `json:"slug,omitempty"`
	Title             string                  `json:"title"`
	VideoID           string                  `json:"video_id,omitempty"`
	VideoTitle        string                  `json:"video_title,omitempty"`
	KnowledgeType     knowledge.KnowledgeType `json:"knowledge_type"`
	EntitySubType     string                  `json:"entity_sub_type,omitempty"`
	PageType          string                  `json:"page_type,omitempty"`
	CoreContent       string                  `json:"core_content,omitempty"`
	StructureFields   []knowledge.DetailField `json:"structure_fields,omitempty"`
	EvidenceIDs       []string                `json:"evidence_ids,omitempty"`
	InformationNature string                  `json:"information_nature,omitempty"`
	TimeRange         string                  `json:"time_range,omitempty"`
	RelatedKnowledge  []knowledge.DetailLink  `json:"related_knowledge,omitempty"`
	RelatedEntities   []knowledge.DetailLink  `json:"related_entities,omitempty"`
}

type EntityGraphNode struct {
	ID              string                      `json:"id"`
	Name            string                      `json:"name"`
	Label           string                      `json:"label"`
	Type            string                      `json:"type"`
	Attributes      []string                    `json:"attributes"`
	KnowledgeID     string                      `json:"knowledge_id,omitempty"`
	VideoID         string                      `json:"video_id,omitempty"`
	VideoTitle      string                      `json:"video_title,omitempty"`
	VideoType       string                      `json:"video_category,omitempty"`
	Seconds         int                         `json:"seconds,omitempty"`
	LinkCount       int                         `json:"link_count"`
	Evidence        []EntityGraphEvidence       `json:"evidence,omitempty"`
	KnowledgeDetail *EntityGraphKnowledgeDetail `json:"knowledge_detail,omitempty"`
}

type EntityGraphEdge struct {
	ID         string  `json:"id"`
	Source     string  `json:"source"`
	Target     string  `json:"target"`
	Type       string  `json:"type"`
	Weight     int     `json:"weight"`
	Confidence float64 `json:"confidence,omitempty"`
}

type entityGraphResponse struct {
	Nodes      []EntityGraphNode            `json:"nodes"`
	Edges      []EntityGraphEdge            `json:"edges"`
	WikiPages  []EntityGraphKnowledgeDetail `json:"wiki_pages,omitempty"`
	Attributes []string                     `json:"attributes"`
	Meta       struct {
		Mode      string `json:"mode"`
		Total     int    `json:"total"`
		Returned  int    `json:"returned"`
		Truncated bool   `json:"truncated"`
	} `json:"meta"`
}

type EntityGraphHandler struct {
	db    *gorm.DB
	graph graphSource
	wiki  *weknora.WikiClient
	kbID  string
}

func NewEntityGraphHandler(db *gorm.DB, graph *weknora.Client, kbID string, wiki ...*weknora.WikiClient) *EntityGraphHandler {
	var wikiClient *weknora.WikiClient
	if len(wiki) > 0 {
		wikiClient = wiki[0]
	}
	return &EntityGraphHandler{db: db, graph: graph, wiki: wikiClient, kbID: strings.TrimSpace(kbID)}
}

func (h *EntityGraphHandler) Get(c *gin.Context) {
	if h.graph == nil || strings.TrimSpace(h.kbID) == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "error": "entity graph is unavailable"})
		return
	}
	limit := 500
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "limit must be a positive integer"})
			return
		}
		limit = parsed
	}
	if limit > 500 {
		limit = 500
	}
	requestedTypes := splitQueryValues(c.Query("types"))
	requestedVideoID := strings.TrimSpace(c.Query("video_id"))
	graphData, err := h.graph.GetEntityGraph(c.Request.Context(), h.kbID, 2000, nil)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"success": false, "error": err.Error()})
		return
	}
	if graphData == nil {
		graphData = &weknora.EntityGraphData{}
	}
	response, err := h.buildResponse(c.Request.Context(), graphData, requestedTypes, requestedVideoID, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": response})
}

func (h *EntityGraphHandler) buildResponse(ctx context.Context, source *weknora.EntityGraphData, requestedTypes []string, requestedVideoID string, limit int) (*entityGraphResponse, error) {
	knowledgeIDs := make([]string, 0, len(source.Nodes))
	seenKnowledgeIDs := make(map[string]struct{}, len(source.Nodes))
	for _, node := range source.Nodes {
		if node == nil || node.KnowledgeID == "" {
			continue
		}
		if _, exists := seenKnowledgeIDs[node.KnowledgeID]; !exists {
			seenKnowledgeIDs[node.KnowledgeID] = struct{}{}
			knowledgeIDs = append(knowledgeIDs, node.KnowledgeID)
		}
	}

	chunkByKnowledgeID := make(map[string]model.VideoTranscriptChunk, len(knowledgeIDs))
	videoByID := make(map[string]model.Video, len(knowledgeIDs))
	if h.db != nil && len(knowledgeIDs) > 0 {
		var chunks []model.VideoTranscriptChunk
		if err := h.db.WithContext(ctx).Where("knowledge_id IN ?", knowledgeIDs).Find(&chunks).Error; err != nil {
			return nil, fmt.Errorf("load transcript evidence: %w", err)
		}
		videoIDs := make([]string, 0, len(chunks))
		seenVideoIDs := make(map[string]struct{}, len(chunks))
		for _, chunk := range chunks {
			chunkByKnowledgeID[chunk.KnowledgeID] = chunk
			if _, exists := seenVideoIDs[chunk.VideoID]; !exists {
				seenVideoIDs[chunk.VideoID] = struct{}{}
				videoIDs = append(videoIDs, chunk.VideoID)
			}
		}
		if len(videoIDs) > 0 {
			var videos []model.Video
			if err := h.db.WithContext(ctx).Where("id IN ?", videoIDs).Find(&videos).Error; err != nil {
				return nil, fmt.Errorf("load graph videos: %w", err)
			}
			for _, video := range videos {
				videoByID[video.ID] = video
			}
		}
	}
	if h.db != nil && h.wiki != nil {
		var wikiVideos []model.Video
		if err := h.db.WithContext(ctx).
			Where("knowledge_base_wiki_page_id <> ?", "").
			Limit(200).
			Find(&wikiVideos).Error; err != nil {
			return nil, fmt.Errorf("load graph wiki videos: %w", err)
		}
		for _, video := range wikiVideos {
			if _, exists := videoByID[video.ID]; !exists {
				videoByID[video.ID] = video
			}
		}
	}
	detailByNode := make(map[string]*EntityGraphKnowledgeDetail)
	wikiPages := make([]EntityGraphKnowledgeDetail, 0)
	if h.wiki != nil && strings.TrimSpace(h.kbID) != "" && len(videoByID) > 0 {
		var err error
		detailByNode, wikiPages, err = h.loadKnowledgeDetails(ctx, videoByID)
		if err != nil {
			return nil, fmt.Errorf("load graph knowledge details: %w", err)
		}
	}

	result := &entityGraphResponse{}
	result.WikiPages = wikiPages
	result.Meta.Mode = "overview"
	allNodes := make([]EntityGraphNode, 0, len(source.Nodes))
	nodeIDByKey := make(map[string]string, len(source.Nodes))
	nodeIDByName := make(map[string]string, len(source.Nodes))
	for _, node := range source.Nodes {
		if node == nil || strings.TrimSpace(node.Name) == "" {
			continue
		}
		typeName := graphType(node.Attributes)
		if len(requestedTypes) > 0 && !contains(requestedTypes, typeName) {
			continue
		}
		chunk, hasChunk := chunkByKnowledgeID[node.KnowledgeID]
		video, hasVideo := videoByID[chunk.VideoID]
		if requestedVideoID != "" && (!hasChunk || chunk.VideoID != requestedVideoID) {
			continue
		}
		id := graphNodeID(node.KnowledgeID, node.Name)
		item := EntityGraphNode{
			ID: id, Name: node.Name, Label: node.Name, Type: typeName,
			Attributes: []string{typeName}, KnowledgeID: node.KnowledgeID,
			LinkCount: 0,
		}
		if hasVideo {
			item.VideoID = video.ID
			item.VideoTitle = video.Title
			item.VideoType = video.VideoType
		}
		if hasChunk {
			item.KnowledgeDetail = detailByNode[graphDetailKey(chunk.VideoID, node.Name)]
		}
		if hasChunk {
			item.Seconds = chunk.StartMs / 1000
			item.Evidence = []EntityGraphEvidence{{
				VideoID: chunk.VideoID, VideoTitle: video.Title, StartMs: chunk.StartMs, EndMs: chunk.EndMs,
				ChunkIndex: chunk.ChunkIndex, ChunkIDs: append([]string(nil), node.Chunks...),
			}}
		}
		allNodes = append(allNodes, item)
		if node.KnowledgeID != "" {
			nodeIDByKey[node.KnowledgeID+"|"+node.Name] = id
		}
		if _, exists := nodeIDByName[node.Name]; !exists {
			nodeIDByName[node.Name] = id
		}
	}
	result.Meta.Total = len(allNodes)
	if len(allNodes) > limit {
		allNodes = allNodes[:limit]
		result.Meta.Truncated = true
	}
	result.Meta.Returned = len(allNodes)
	result.Nodes = allNodes

	visible := make(map[string]struct{}, len(allNodes))
	for _, node := range allNodes {
		visible[node.ID] = struct{}{}
	}
	for _, relation := range source.Edges {
		if relation == nil {
			continue
		}
		sourceID := nodeIDByKey[relation.SourceKnowledgeID+"|"+relation.Node1]
		if sourceID == "" {
			sourceID = nodeIDByName[relation.Node1]
		}
		targetID := nodeIDByKey[relation.TargetKnowledgeID+"|"+relation.Node2]
		if targetID == "" {
			targetID = nodeIDByName[relation.Node2]
		}
		if sourceID == "" || targetID == "" || sourceID == targetID {
			continue
		}
		if _, ok := visible[sourceID]; !ok {
			continue
		}
		if _, ok := visible[targetID]; !ok {
			continue
		}
		result.Edges = append(result.Edges, EntityGraphEdge{ID: sourceID + "->" + targetID + ":" + relation.Type, Source: sourceID, Target: targetID, Type: relation.Type, Weight: 1})
	}
	for index := range result.Nodes {
		for _, edge := range result.Edges {
			if edge.Source == result.Nodes[index].ID || edge.Target == result.Nodes[index].ID {
				result.Nodes[index].LinkCount++
			}
		}
	}
	attributes := []string{"实体", "概念", "案例", "方法", "洞察"}
	for _, typeName := range attributes {
		for _, node := range result.Nodes {
			if node.Type == typeName {
				result.Attributes = append(result.Attributes, typeName)
				break
			}
		}
	}
	return result, nil
}

func (h *EntityGraphHandler) loadKnowledgeDetails(ctx context.Context, videos map[string]model.Video) (map[string]*EntityGraphKnowledgeDetail, []EntityGraphKnowledgeDetail, error) {
	details := make(map[string]*EntityGraphKnowledgeDetail)
	wikiPages := make([]EntityGraphKnowledgeDetail, 0)
	seenPageIDs := make(map[string]struct{})
	for _, video := range videos {
		if strings.TrimSpace(video.ID) == "" || strings.TrimSpace(video.KnowledgeBaseWikiPageID) == "" {
			continue
		}
		knowledgeBasePage, err := h.wiki.GetPageByID(ctx, h.kbID, video.KnowledgeBaseWikiPageID)
		if err != nil {
			return nil, nil, fmt.Errorf("read knowledge_base wiki page for video %s: %w", video.ID, err)
		}
		if !isKnowledgeBaseWikiPage(knowledgeBasePage, video.ID) {
			continue
		}
		pages, err := h.wiki.ListByVideoOwned(ctx, h.kbID, video.ID, relatedKnowledgePageTypes, knowledgeBasePage)
		if err != nil {
			return nil, nil, fmt.Errorf("list knowledge pages for video %s: %w", video.ID, err)
		}
		for _, page := range pages {
			if page.ID == knowledgeBasePage.ID {
				continue
			}
			detail := graphKnowledgeDetail(page)
			if detail == nil {
				continue
			}
			detail.VideoID = video.ID
			detail.VideoTitle = video.Title
			if _, exists := seenPageIDs[detail.ID]; !exists {
				seenPageIDs[detail.ID] = struct{}{}
				wikiPages = append(wikiPages, *detail)
			}
			for _, name := range graphDetailNames(page) {
				key := graphDetailKey(video.ID, name)
				if key == "" {
					continue
				}
				if _, exists := details[key]; !exists {
					details[key] = detail
				}
			}
		}
	}
	return details, wikiPages, nil
}

func graphKnowledgeDetail(page weknora.WikiPage) *EntityGraphKnowledgeDetail {
	frontmatter := page.ParsedFrontmatter()
	fmType, _ := frontmatter["type"].(string)
	entitySubType, _ := frontmatter["entity_sub_type"].(string)
	if entitySubType == "" && knowledge.IsEntitySubType(fmType) {
		entitySubType = fmType
	}
	mappedType := knowledge.MapPageTypeToKnowledgeType(page.PageType, fmType)
	if !knowledge.IsKnowledgeType(mappedType) {
		return nil
	}
	parsed := wikiKnowledgeDetail(page.Content, mappedType, entitySubType)
	title := firstNonEmpty(page.Title, frontmatterString(frontmatter, "title"), frontmatterString(frontmatter, "canonical_name"), firstMarkdownHeading(page.Content), page.Slug)
	return &EntityGraphKnowledgeDetail{
		ID: page.ID, Slug: page.Slug, Title: title, KnowledgeType: mappedType, EntitySubType: entitySubType, PageType: page.PageType,
		CoreContent: parsed.CoreContent, StructureFields: parsed.StructureFields, EvidenceIDs: parsed.EvidenceIDs, InformationNature: parsed.InformationNature,
		TimeRange: parsed.TimeRange, RelatedKnowledge: parsed.RelatedKnowledge, RelatedEntities: parsed.RelatedEntities,
	}
}

func graphDetailNames(page weknora.WikiPage) []string {
	frontmatter := page.ParsedFrontmatter()
	candidates := []string{page.Title, frontmatterString(frontmatter, "title"), frontmatterString(frontmatter, "canonical_name"), frontmatterString(frontmatter, "id"), firstMarkdownHeading(page.Content), slugBase(page.Slug)}
	if aliases, ok := frontmatter["aliases"].([]any); ok {
		for _, alias := range aliases {
			if value, ok := alias.(string); ok {
				candidates = append(candidates, value)
			}
		}
	}
	return uniqueNonEmpty(candidates)
}

func firstMarkdownHeading(content string) string {
	body := stripWikiFrontmatter(content)
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	return ""
}

func frontmatterString(frontmatter map[string]any, key string) string {
	value, _ := frontmatter[key].(string)
	return strings.TrimSpace(value)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func uniqueNonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := normalizeGraphDetailName(value)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func slugBase(slug string) string {
	slug = strings.TrimSpace(strings.Trim(slug, "/"))
	if slug == "" {
		return ""
	}
	parts := strings.Split(slug, "/")
	return parts[len(parts)-1]
}

func graphDetailKey(videoID, name string) string {
	if videoID = strings.TrimSpace(videoID); videoID == "" {
		return ""
	}
	name = normalizeGraphDetailName(name)
	if name == "" {
		return ""
	}
	return videoID + "|" + name
}

func normalizeGraphDetailName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimPrefix(name, "[[")
	name = strings.TrimSuffix(name, "]]")
	if separator := strings.IndexByte(name, '|'); separator >= 0 {
		name = name[:separator]
	}
	name = strings.ReplaceAll(name, "　", " ")
	name = strings.Join(strings.Fields(name), " ")
	return strings.ToLower(name)
}

func splitQueryValues(value string) []string {
	values := make([]string, 0)
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			values = append(values, item)
		}
	}
	return values
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func graphNodeID(knowledgeID, name string) string {
	if knowledgeID == "" {
		return name
	}
	return knowledgeID + "|" + name
}

func graphType(attributes []string) string {
	for _, attribute := range attributes {
		normalized := strings.ToLower(strings.TrimSpace(attribute))
		switch normalized {
		case "entity", "实体", "person", "organization", "机构", "人物", "产品", "技术", "行业", "地点":
			return "实体"
		case "concept", "概念":
			return "概念"
		case "case", "案例", "事件":
			return "案例"
		case "method", "methodology", "方法", "方法论":
			return "方法"
		case "insight", "洞察":
			return "洞察"
		}
	}
	return "概念"
}
