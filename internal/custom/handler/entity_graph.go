package handler

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
	"github.com/Tencent/WeKnora/internal/custom/model"
	"github.com/Tencent/WeKnora/internal/custom/service/knowledge"
	"github.com/Tencent/WeKnora/internal/custom/service/knowledgegraph"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type EntityGraphEvidence struct {
	VideoID    string   `json:"video_id"`
	VideoTitle string   `json:"video_title"`
	StartMs    int      `json:"start_ms"`
	EndMs      int      `json:"end_ms"`
	ChunkIndex int      `json:"chunk_index"`
	ChunkIDs   []string `json:"chunk_ids,omitempty"`
}

type EntityGraphKnowledgeDetail struct {
	ID                       string                         `json:"id"`
	KnowledgeObjectID        string                         `json:"knowledge_object_id,omitempty"`
	Slug                     string                         `json:"slug,omitempty"`
	Title                    string                         `json:"title"`
	VideoID                  string                         `json:"video_id,omitempty"`
	VideoTitle               string                         `json:"video_title,omitempty"`
	SourceVideoTitle         string                         `json:"source_video_title,omitempty"`
	Timestamp                string                         `json:"timestamp,omitempty"`
	Seconds                  int                            `json:"seconds,omitempty"`
	KnowledgeType            knowledge.KnowledgeType        `json:"knowledge_type"`
	PrimaryType              knowledge.KnowledgeType        `json:"primary_type,omitempty"`
	EntitySubType            string                         `json:"entity_sub_type,omitempty"`
	PageType                 string                         `json:"page_type,omitempty"`
	CoreContent              string                         `json:"core_content,omitempty"`
	StructureFields          []knowledge.DetailField        `json:"structure_fields,omitempty"`
	EvidenceIDs              []string                       `json:"evidence_ids,omitempty"`
	InformationNature        string                         `json:"information_nature,omitempty"`
	TimeRange                string                         `json:"time_range,omitempty"`
	TranscriptGeneration     string                         `json:"transcript_generation,omitempty"`
	AuditStatus              string                         `json:"audit_status,omitempty"`
	ClassificationConfidence float64                        `json:"classification_confidence,omitempty"`
	Relations                []knowledge.StructuredRelation `json:"relations,omitempty"`
	RelatedKnowledge         []knowledge.DetailLink         `json:"related_knowledge,omitempty"`
	RelatedEntities          []knowledge.DetailLink         `json:"related_entities,omitempty"`
	RelatedContent           []knowledge.DetailLink         `json:"related_content,omitempty"`
}

type EntityGraphNode struct {
	ID                string                      `json:"id"`
	Name              string                      `json:"name"`
	Label             string                      `json:"label"`
	Type              string                      `json:"type"`
	KnowledgeType     knowledge.KnowledgeType     `json:"knowledge_type"`
	Attributes        []string                    `json:"attributes"`
	KnowledgeID       string                      `json:"knowledge_id,omitempty"`
	WikiPageID        string                      `json:"wiki_page_id"`
	KnowledgeObjectID string                      `json:"knowledge_object_id"`
	AuditStatus       string                      `json:"audit_status"`
	VideoID           string                      `json:"video_id,omitempty"`
	VideoTitle        string                      `json:"video_title,omitempty"`
	VideoType         string                      `json:"video_category,omitempty"`
	Seconds           int                         `json:"seconds,omitempty"`
	LinkCount         int                         `json:"link_count"`
	IsOrphan          bool                        `json:"is_orphan"`
	Evidence          []EntityGraphEvidence       `json:"evidence,omitempty"`
	KnowledgeDetail   *EntityGraphKnowledgeDetail `json:"knowledge_detail,omitempty"`
}

type EntityGraphEdge struct {
	ID             string   `json:"id"`
	Source         string   `json:"source"`
	Target         string   `json:"target"`
	Type           string   `json:"type"`
	Weight         int      `json:"weight"`
	Confidence     float64  `json:"confidence,omitempty"`
	EvidenceIDs    []string `json:"evidence_ids,omitempty"`
	RelationKind   string   `json:"relation_kind"`
	RelationSource string   `json:"relation_source"`
	Counted        bool     `json:"counted"`
}

// EntityGraphReadingAssociation is a navigational Wiki link, not a semantic
// conclusion. It is intentionally returned separately from formal graph edges
// and is never written to the Neo4j projection.
type EntityGraphReadingAssociation struct {
	ID             string `json:"id"`
	Source         string `json:"source"`
	Target         string `json:"target"`
	RelationKind   string `json:"relation_kind"`
	RelationSource string `json:"relation_source"`
	TargetExists   bool   `json:"target_exists"`
	Counted        bool   `json:"counted"`
}

type entityGraphResponse struct {
	KnowledgeBaseID     string                          `json:"knowledge_base_id"`
	Nodes               []EntityGraphNode               `json:"nodes"`
	Edges               []EntityGraphEdge               `json:"edges"`
	ReadingAssociations []EntityGraphReadingAssociation `json:"reading_associations"`
	WikiPages           []EntityGraphKnowledgeDetail    `json:"wiki_pages,omitempty"`
	Attributes          []string                        `json:"attributes"`
	Meta                struct {
		Mode                    string `json:"mode"`
		Total                   int    `json:"total"`
		Returned                int    `json:"returned"`
		Truncated               bool   `json:"truncated"`
		SemanticEdgeCount       int    `json:"semantic_edge_count"`
		ReadingAssociationCount int    `json:"reading_association_count"`
	} `json:"meta"`
}

type EntityGraphHandler struct {
	db    *gorm.DB
	graph knowledgegraph.Store
	wiki  *weknora.WikiClient
	kbID  string
}

func NewEntityGraphHandler(db *gorm.DB, graph knowledgegraph.Store, kbID string, wiki ...*weknora.WikiClient) *EntityGraphHandler {
	var wikiClient *weknora.WikiClient
	if len(wiki) > 0 {
		wikiClient = wiki[0]
	}
	return &EntityGraphHandler{db: db, graph: graph, wiki: wikiClient, kbID: strings.TrimSpace(kbID)}
}

func (h *EntityGraphHandler) Get(c *gin.Context) {
	if h.graph == nil || h.wiki == nil || strings.TrimSpace(h.kbID) == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "error": "Wiki knowledge graph is unavailable"})
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
	types, err := parseGraphTypes(c.Query("types"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": err.Error()})
		return
	}
	source, err := h.graph.Query(c.Request.Context(), knowledgegraph.Query{
		VideoID: strings.TrimSpace(c.Query("video_id")), Types: types, Limit: limit,
	})
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"success": false, "error": err.Error()})
		return
	}
	if source == nil || len(source.Nodes) == 0 {
		if err := h.graph.ProjectKnowledgeBase(c.Request.Context()); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"success": false, "error": err.Error()})
			return
		}
		source, err = h.graph.Query(c.Request.Context(), knowledgegraph.Query{
			VideoID: strings.TrimSpace(c.Query("video_id")), Types: types, Limit: limit,
		})
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"success": false, "error": err.Error()})
			return
		}
	}
	response, err := h.buildResponse(c.Request.Context(), source, limit)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"success": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": response})
}

func (h *EntityGraphHandler) buildResponse(ctx context.Context, source *knowledgegraph.Graph, limit int) (*entityGraphResponse, error) {
	if source == nil {
		source = &knowledgegraph.Graph{}
	}
	videoIDs := make([]string, 0)
	seenVideoIDs := make(map[string]struct{})
	for _, node := range source.Nodes {
		addVideoID(&videoIDs, seenVideoIDs, node.SourceVideoID)
		for _, evidenceID := range node.EvidenceIDs {
			ref := parseTranscriptEvidenceRef(evidenceID)
			addVideoID(&videoIDs, seenVideoIDs, ref.VideoID)
		}
	}
	videoByID := make(map[string]model.Video, len(videoIDs))
	chunkByEvidence := make(map[string]model.VideoTranscriptChunk)
	chunkByIndex := make(map[string]model.VideoTranscriptChunk)
	if h.db != nil && len(videoIDs) > 0 {
		var videos []model.Video
		if err := h.db.WithContext(ctx).Where("id IN ?", videoIDs).Find(&videos).Error; err != nil {
			return nil, fmt.Errorf("load graph videos: %w", err)
		}
		for _, video := range videos {
			videoByID[video.ID] = video
		}
		var chunks []model.VideoTranscriptChunk
		if err := h.db.WithContext(ctx).Where("video_id IN ? AND status = ?", videoIDs, "completed").Find(&chunks).Error; err != nil {
			return nil, fmt.Errorf("load graph evidence: %w", err)
		}
		for _, chunk := range chunks {
			key := chunk.VideoID + "\x00" + chunk.Generation + "\x00" + chunk.KnowledgeID
			chunkByEvidence[key] = chunk
			indexKey := chunk.VideoID + "\x00" + chunk.Generation + "\x00" + strconv.Itoa(chunk.ChunkIndex)
			chunkByIndex[indexKey] = chunk
		}
	}

	result := &entityGraphResponse{KnowledgeBaseID: h.kbID}
	result.Meta.Mode = "overview"
	result.Meta.Total = len(source.Nodes)
	result.Nodes = make([]EntityGraphNode, 0)
	result.Edges = make([]EntityGraphEdge, 0)
	result.WikiPages = make([]EntityGraphKnowledgeDetail, 0)
	pageByID := make(map[string]weknora.WikiPage)
	if h.wiki != nil && len(source.Nodes) > 0 {
		pages, err := h.wiki.ListAllPages(ctx, h.kbID, "")
		if err != nil {
			return nil, fmt.Errorf("load Wiki pages for graph details: %w", err)
		}
		for _, page := range pages {
			pageByID[page.ID] = page
		}
	}
	nodeByPageID := make(map[string]EntityGraphNode, len(source.Nodes))
	for _, projected := range source.Nodes {
		if projected.WikiPageID == "" {
			continue
		}
		page, ok := pageByID[projected.WikiPageID]
		if !ok || strings.TrimSpace(page.Content) == "" {
			var (
				loaded *weknora.WikiPage
				err    error
			)
			if ok && strings.TrimSpace(page.Slug) != "" {
				loaded, err = h.wiki.GetPage(ctx, h.kbID, page.Slug)
			} else {
				loaded, err = h.wiki.GetPageByID(ctx, h.kbID, projected.WikiPageID)
			}
			if err != nil {
				return nil, fmt.Errorf("read Wiki page %s: %w", projected.WikiPageID, err)
			}
			if loaded == nil {
				continue
			}
			page = *loaded
		}
		if strings.TrimSpace(page.Content) == "" {
			continue
		}
		if page.ID == "" {
			page.ID = projected.WikiPageID
		}
		if pageByID[projected.WikiPageID].ID == "" || strings.TrimSpace(pageByID[projected.WikiPageID].Content) == "" {
			pageByID[projected.WikiPageID] = page
		}
		detail := graphKnowledgeDetail(page)
		if detail == nil || detail.KnowledgeType != projected.KnowledgeType {
			continue
		}
		if detail.KnowledgeObjectID == "" {
			detail.KnowledgeObjectID = projected.KnowledgeObjectID
		}
		if detail.TranscriptGeneration == "" {
			detail.TranscriptGeneration = projected.TranscriptGeneration
		}
		if detail.AuditStatus == "" {
			detail.AuditStatus = projected.AuditStatus
		}
		if detail.KnowledgeObjectID != projected.KnowledgeObjectID ||
			detail.TranscriptGeneration != projected.TranscriptGeneration ||
			strings.ToLower(detail.AuditStatus) != strings.ToLower(projected.AuditStatus) {
			continue
		}
		if !displayableGraphDetail(detail) {
			continue
		}
		detail.VideoID = projected.SourceVideoID
		if video, ok := videoByID[projected.SourceVideoID]; ok {
			detail.VideoTitle = video.Title
			detail.SourceVideoTitle = video.Title
		}
		if timestamp, seconds := wikiAnchorTimeline(detail.TimeRange); timestamp != "" {
			detail.Timestamp = timestamp
			detail.Seconds = seconds
		}
		if detail.CoreContent == "" {
			detail.CoreContent = projected.Summary
		}
		if len(detail.EvidenceIDs) == 0 {
			detail.EvidenceIDs = append([]string(nil), projected.EvidenceIDs...)
		}
		if detail.ClassificationConfidence == 0 {
			detail.ClassificationConfidence = projected.ClassificationConfidence
		}
		enrichStructuredRelationTargets(detail.Relations, pageByID)
		title := detail.Title
		if title == "" {
			title = projected.Title
		}

		item := EntityGraphNode{
			ID: projected.ID, Name: title, Label: title,
			Type:          knowledgeTypeLabel(projected.KnowledgeType),
			KnowledgeType: projected.KnowledgeType, Attributes: []string{knowledgeTypeLabel(projected.KnowledgeType)},
			KnowledgeID: firstEvidenceID(detail.EvidenceIDs), WikiPageID: projected.WikiPageID,
			KnowledgeObjectID: projected.KnowledgeObjectID, AuditStatus: projected.AuditStatus,
			KnowledgeDetail: detail, LinkCount: 0,
		}
		if video, ok := videoByID[projected.SourceVideoID]; ok {
			item.VideoID, item.VideoTitle, item.VideoType = video.ID, video.Title, video.VideoType
		}
		for _, evidenceID := range detail.EvidenceIDs {
			chunk, ok := resolveEvidenceChunk(
				chunkByEvidence,
				chunkByIndex,
				projected.SourceVideoID,
				projected.TranscriptGeneration,
				evidenceID,
			)
			if !ok {
				continue
			}
			if item.VideoID == "" {
				item.VideoID = chunk.VideoID
			}
			if video, ok := videoByID[chunk.VideoID]; ok {
				item.VideoTitle = video.Title
				item.VideoType = video.VideoType
				detail.VideoID = video.ID
				detail.VideoTitle = video.Title
				detail.SourceVideoTitle = video.Title
			}
			item.Evidence = append(item.Evidence, EntityGraphEvidence{
				VideoID: chunk.VideoID, VideoTitle: videoByID[chunk.VideoID].Title,
				StartMs: chunk.StartMs, EndMs: chunk.EndMs, ChunkIndex: chunk.ChunkIndex,
				ChunkIDs: []string{chunk.KnowledgeID},
			})
		}
		if len(item.Evidence) > 0 {
			item.Seconds = item.Evidence[0].StartMs / 1000
			if detail.Timestamp == "" {
				detail.Timestamp = formatGraphTimestamp(item.Seconds)
				detail.Seconds = item.Seconds
			}
		} else if detail.Seconds > 0 || detail.Timestamp != "" {
			item.Seconds = detail.Seconds
		}
		nodeByPageID[item.WikiPageID] = item
	}

	nodes := make([]EntityGraphNode, 0, len(nodeByPageID))
	for _, node := range nodeByPageID {
		nodes = append(nodes, node)
	}
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].Label == nodes[j].Label {
			return nodes[i].WikiPageID < nodes[j].WikiPageID
		}
		return nodes[i].Label < nodes[j].Label
	})
	if len(nodes) > limit {
		nodes = nodes[:limit]
		result.Meta.Truncated = true
	}
	result.Nodes = nodes
	result.Meta.Returned = len(nodes)
	result.ReadingAssociations = make([]EntityGraphReadingAssociation, 0)
	visible := make(map[string]struct{}, len(nodes))
	for index := range result.Nodes {
		visible[result.Nodes[index].WikiPageID] = struct{}{}
	}
	// Formal link_count deliberately excludes Wiki navigation links. This keeps
	// orphan detection meaningful even when a page has only historical links.
	for _, edge := range source.Edges {
		if weakWikiLinkEdge(edge) {
			continue
		}
		if _, sourceOK := visible[edge.SourceWikiPageID]; !sourceOK {
			continue
		}
		if _, targetOK := visible[edge.TargetWikiPageID]; !targetOK {
			continue
		}
		result.Edges = append(result.Edges, EntityGraphEdge{
			ID: edge.ID, Source: "wiki:" + edge.SourceWikiPageID, Target: "wiki:" + edge.TargetWikiPageID,
			Type: edge.RelationType, Weight: 1, Confidence: edge.Confidence, EvidenceIDs: edge.EvidenceIDs,
			RelationKind: "semantic", RelationSource: "skill", Counted: true,
		})
	}
	result.Meta.SemanticEdgeCount = len(result.Edges)
	for index := range result.Nodes {
		for _, edge := range result.Edges {
			if edge.Source == result.Nodes[index].ID || edge.Target == result.Nodes[index].ID {
				result.Nodes[index].LinkCount++
			}
		}
		result.Nodes[index].IsOrphan = result.Nodes[index].LinkCount == 0
	}
	result.ReadingAssociations = buildReadingAssociations(result.Nodes, pageByID)
	result.Meta.ReadingAssociationCount = len(result.ReadingAssociations)
	result.WikiPages = make([]EntityGraphKnowledgeDetail, 0, len(result.Nodes))
	for _, node := range result.Nodes {
		if node.KnowledgeDetail != nil {
			result.WikiPages = append(result.WikiPages, *node.KnowledgeDetail)
		}
	}
	result.Attributes = []string{"实体", "概念", "案例", "方法论", "洞察"}
	return result, nil
}

func formatGraphTimestamp(seconds int) string {
	if seconds < 0 {
		seconds = 0
	}
	hours := seconds / 3600
	minutes := (seconds % 3600) / 60
	remainder := seconds % 60
	if hours > 0 {
		return fmt.Sprintf("%02d:%02d:%02d", hours, minutes, remainder)
	}
	return fmt.Sprintf("%02d:%02d", minutes, remainder)
}

func graphKnowledgeDetail(page weknora.WikiPage) *EntityGraphKnowledgeDetail {
	frontmatter := page.ParsedFrontmatter()
	fmType := knowledgeObjectType(frontmatter)
	entitySubType := frontmatterString(frontmatter, "entity_sub_type")
	if entitySubType == "" && knowledge.IsEntitySubType(fmType) {
		entitySubType = fmType
	}
	mappedType := graphPageKnowledgeType(page.PageType, fmType)
	if !knowledge.IsKnowledgeType(mappedType) {
		return nil
	}
	parsed := wikiKnowledgeDetail(page.Content, mappedType, entitySubType)
	mergeStructuredFrontmatter(&parsed, frontmatter, mappedType, entitySubType)
	title := firstNonEmpty(frontmatterString(frontmatter, "title"), frontmatterString(frontmatter, "canonical_name"), firstMarkdownHeading(page.Content), page.Title, page.Slug)
	return &EntityGraphKnowledgeDetail{
		ID: page.ID, KnowledgeObjectID: frontmatterString(frontmatter, "knowledge_object_id"),
		Slug: page.Slug, Title: title, KnowledgeType: mappedType, PrimaryType: mappedType, EntitySubType: entitySubType, PageType: page.PageType,
		CoreContent: parsed.CoreContent, StructureFields: parsed.StructureFields, EvidenceIDs: parsed.EvidenceIDs,
		InformationNature: firstNonEmpty(parsed.InformationNature, informationNatureLabel(mappedType, entitySubType)), TimeRange: parsed.TimeRange,
		TranscriptGeneration:     frontmatterString(frontmatter, "transcript_generation"),
		AuditStatus:              frontmatterString(frontmatter, "audit_status"),
		ClassificationConfidence: frontmatterFloat(frontmatter, "classification_confidence"),
		Relations:                parsed.Relations,
		RelatedContent:           parsed.RelatedContent,
	}
}

func graphPageKnowledgeType(pageType string, frontmatterType string) knowledge.KnowledgeType {
	if mapped := knowledge.MapPageTypeToKnowledgeType(pageType, frontmatterType); mapped != "" {
		return mapped
	}
	switch strings.ToLower(strings.TrimSpace(pageType)) {
	case "case":
		return knowledge.TypeCase
	case "method", "methodology":
		return knowledge.TypeMethodology
	case "insight":
		return knowledge.TypeInsight
	default:
		return ""
	}
}

func parseGraphTypes(value string) ([]knowledge.KnowledgeType, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	out := make([]knowledge.KnowledgeType, 0)
	for _, raw := range strings.Split(value, ",") {
		raw = strings.ToLower(strings.TrimSpace(raw))
		if raw == "" {
			continue
		}
		switch raw {
		case "entity", "实体":
			out = append(out, knowledge.TypeEntity)
		case "concept", "概念":
			out = append(out, knowledge.TypeConcept)
		case "case", "案例":
			out = append(out, knowledge.TypeCase)
		case "methodology", "方法论":
			out = append(out, knowledge.TypeMethodology)
		case "insight", "洞察":
			out = append(out, knowledge.TypeInsight)
		default:
			return nil, fmt.Errorf("unsupported graph knowledge type: %s", raw)
		}
	}
	return out, nil
}

func knowledgeTypeLabel(value knowledge.KnowledgeType) string {
	switch value {
	case knowledge.TypeEntity:
		return "实体"
	case knowledge.TypeConcept:
		return "概念"
	case knowledge.TypeCase:
		return "案例"
	case knowledge.TypeMethodology:
		return "方法论"
	case knowledge.TypeInsight:
		return "洞察"
	default:
		return ""
	}
}

func firstEvidenceID(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
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

func frontmatterString(frontmatter map[string]any, key string) string {
	value, _ := frontmatter[key].(string)
	return strings.TrimSpace(value)
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func addVideoID(videoIDs *[]string, seen map[string]struct{}, videoID string) {
	videoID = strings.TrimSpace(videoID)
	if videoID == "" {
		return
	}
	if _, exists := seen[videoID]; exists {
		return
	}
	seen[videoID] = struct{}{}
	*videoIDs = append(*videoIDs, videoID)
}

type transcriptEvidenceRef struct {
	VideoID     string
	Generation  string
	KnowledgeID string
	ChunkIndex  int
	HasIndex    bool
}

func parseTranscriptEvidenceRef(value string) transcriptEvidenceRef {
	value = strings.TrimSpace(value)
	ref := transcriptEvidenceRef{KnowledgeID: value}
	if pipe := strings.Index(value, "|"); pipe >= 0 {
		ref.KnowledgeID = strings.TrimSpace(value[:pipe])
		value = strings.TrimSpace(value[pipe+1:])
	}
	parts := strings.Split(value, "/")
	for index, part := range parts {
		if part != "transcript" || index+3 >= len(parts) {
			continue
		}
		ref.VideoID = strings.TrimSpace(parts[index+1])
		ref.Generation = strings.TrimSpace(parts[index+2])
		if chunkIndex, err := strconv.Atoi(strings.TrimLeft(strings.TrimSpace(parts[index+3]), "0")); err == nil {
			ref.ChunkIndex = chunkIndex
			ref.HasIndex = true
		} else if strings.TrimSpace(parts[index+3]) == "0" || strings.TrimSpace(parts[index+3]) == "000000" {
			ref.HasIndex = true
		}
		return ref
	}
	return ref
}

func resolveEvidenceChunk(
	byEvidence map[string]model.VideoTranscriptChunk,
	byIndex map[string]model.VideoTranscriptChunk,
	sourceVideoID string,
	generation string,
	evidenceID string,
) (model.VideoTranscriptChunk, bool) {
	if chunk, ok := byEvidence[sourceVideoID+"\x00"+generation+"\x00"+evidenceID]; ok {
		return chunk, true
	}
	ref := parseTranscriptEvidenceRef(evidenceID)
	if ref.VideoID != "" && ref.Generation != "" {
		if chunk, ok := byEvidence[ref.VideoID+"\x00"+ref.Generation+"\x00"+ref.KnowledgeID]; ok {
			return chunk, true
		}
		if ref.HasIndex {
			if chunk, ok := byIndex[ref.VideoID+"\x00"+ref.Generation+"\x00"+strconv.Itoa(ref.ChunkIndex)]; ok {
				return chunk, true
			}
		}
	}
	return model.VideoTranscriptChunk{}, false
}

func mergeStructuredFrontmatter(parsed *wikiKnowledgeDetailData, frontmatter map[string]any, knowledgeType knowledge.KnowledgeType, entitySubType string) {
	if parsed == nil {
		return
	}
	if parsed.CoreContent == "" {
		parsed.CoreContent = firstNonEmpty(
			frontmatterString(frontmatter, "core_content"),
			frontmatterString(frontmatter, "summary"),
			frontmatterString(frontmatter, "description"),
		)
	}
	if len(parsed.EvidenceIDs) == 0 {
		parsed.EvidenceIDs = frontmatterStringSlice(frontmatter["evidence_ids"])
	}
	if parsed.InformationNature == "" {
		parsed.InformationNature = frontmatterString(frontmatter, "information_nature")
	}
	if parsed.TimeRange == "" {
		parsed.TimeRange = frontmatterString(frontmatter, "time_range")
	}
	if len(parsed.StructureFields) == 0 {
		parsed.StructureFields = frontmatterStructureFields(frontmatter, knowledgeType, entitySubType)
	}
	if len(parsed.Relations) == 0 {
		parsed.Relations = frontmatterRelations(frontmatter["relations"])
	}
	parsed.RelatedContent = mergeDetailLinks(
		parsed.RelatedContent,
		frontmatterDetailLinks(frontmatter["related_content"]),
		parsed.RelatedKnowledge,
		frontmatterDetailLinks(frontmatter["related_knowledge"]),
		parsed.RelatedEntities,
		frontmatterDetailLinks(frontmatter["related_entities"]),
	)
}

func frontmatterStructureFields(frontmatter map[string]any, knowledgeType knowledge.KnowledgeType, entitySubType string) []knowledge.DetailField {
	values := firstFrontmatterStructureMap(frontmatter, knowledgeType)
	if len(values) == 0 {
		return nil
	}
	fieldSet := string(knowledgeType)
	if knowledgeType == knowledge.TypeEntity && entitySubType != "" {
		fieldSet = entitySubType
	}
	fields := make([]knowledge.DetailField, 0)
	for _, field := range typeFrameworkFields[fieldSet] {
		value := strings.TrimSpace(frontmatterString(values, field.Key))
		if value != "" {
			fields = append(fields, knowledge.DetailField{Key: field.Key, Label: field.Label, Value: value})
		}
	}
	return fields
}

func firstFrontmatterStructureMap(frontmatter map[string]any, knowledgeType knowledge.KnowledgeType) map[string]any {
	keys := []string{"structure_fields"}
	switch knowledgeType {
	case knowledge.TypeEntity:
		keys = append(keys, "key_attributes", "entity_attributes")
	case knowledge.TypeMethodology:
		keys = append(keys, "method_structure", "methodology_structure")
	case knowledge.TypeCase:
		keys = append(keys, "case_structure")
	case knowledge.TypeConcept:
		keys = append(keys, "concept_structure")
	case knowledge.TypeInsight:
		keys = append(keys, "insight_structure")
	}
	for _, key := range keys {
		if values, ok := frontmatter[key].(map[string]any); ok && len(values) > 0 {
			return values
		}
	}
	return nil
}

func weakWikiLinkEdge(edge knowledgegraph.Edge) bool {
	return edge.RelationType == "related_to" && strings.HasPrefix(edge.ID, "wiki-link:")
}

func buildReadingAssociations(nodes []EntityGraphNode, pages map[string]weknora.WikiPage) []EntityGraphReadingAssociation {
	if len(nodes) == 0 || len(pages) == 0 {
		return []EntityGraphReadingAssociation{}
	}
	bySlug := make(map[string]weknora.WikiPage, len(pages))
	byTitle := make(map[string]weknora.WikiPage, len(pages))
	for _, page := range pages {
		if page.ID == "" {
			continue
		}
		if slug := strings.TrimSpace(page.Slug); slug != "" {
			bySlug[slug] = page
		}
		if title := strings.TrimSpace(page.Title); title != "" {
			byTitle[title] = page
		}
	}
	visible := make(map[string]struct{}, len(nodes))
	for _, node := range nodes {
		visible[node.WikiPageID] = struct{}{}
	}
	result := make([]EntityGraphReadingAssociation, 0)
	seen := make(map[string]struct{})
	add := func(sourceID, rawTarget string) {
		rawTarget = strings.TrimSpace(rawTarget)
		if rawTarget == "" {
			return
		}
		target, exists := resolveWikiLink(rawTarget, pages, bySlug, byTitle)
		targetID := rawTarget
		if exists {
			targetID = target.ID
		}
		key := sourceID + "\x00" + targetID
		if _, ok := seen[key]; ok || sourceID == targetID {
			return
		}
		seen[key] = struct{}{}
		result = append(result, EntityGraphReadingAssociation{
			ID:     "wiki-link:" + sourceID + ":" + targetID,
			Source: "wiki:" + sourceID, Target: "wiki:" + targetID,
			RelationKind: "reading", RelationSource: "wiki_link",
			TargetExists: exists, Counted: false,
		})
	}
	for _, node := range nodes {
		page, ok := pages[node.WikiPageID]
		if !ok {
			continue
		}
		links := append([]string(nil), page.OutLinks...)
		links = append(links, wikiLinkTargetsForGraph(page.Content)...)
		for _, link := range links {
			add(node.WikiPageID, link)
		}
	}
	// Some Wiki API versions return only in_links. Use them to recover links
	// whose source is one of the visible graph nodes.
	for _, targetPage := range pages {
		for _, sourceRef := range targetPage.InLinks {
			source, exists := resolveWikiLink(sourceRef, pages, bySlug, byTitle)
			if exists {
				if _, isVisible := visible[source.ID]; isVisible {
					add(source.ID, targetPage.ID)
				}
			}
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Source == result[j].Source {
			return result[i].Target < result[j].Target
		}
		return result[i].Source < result[j].Source
	})
	return result
}

func resolveWikiLink(raw string, pages map[string]weknora.WikiPage, bySlug, byTitle map[string]weknora.WikiPage) (weknora.WikiPage, bool) {
	value := strings.TrimSpace(raw)
	value = strings.TrimPrefix(value, "[[")
	value = strings.TrimSuffix(value, "]]")
	if separator := strings.IndexByte(value, '|'); separator >= 0 {
		value = strings.TrimSpace(value[:separator])
	}
	value = strings.TrimSpace(strings.TrimPrefix(value, "wiki:"))
	if page, ok := pages[value]; ok {
		return page, true
	}
	if page, ok := bySlug[value]; ok {
		return page, true
	}
	if page, ok := byTitle[value]; ok {
		return page, true
	}
	return weknora.WikiPage{}, false
}

func wikiLinkTargetsForGraph(content string) []string {
	targets := make([]string, 0)
	for {
		start := strings.Index(content, "[[")
		if start < 0 {
			return targets
		}
		content = content[start+2:]
		end := strings.Index(content, "]]")
		if end < 0 {
			return targets
		}
		value := strings.TrimSpace(content[:end])
		if separator := strings.IndexByte(value, '|'); separator >= 0 {
			value = strings.TrimSpace(value[:separator])
		}
		if value != "" {
			targets = append(targets, value)
		}
		content = content[end+2:]
	}
}

func displayableGraphDetail(detail *EntityGraphKnowledgeDetail) bool {
	if detail == nil {
		return false
	}
	if detail.KnowledgeType != knowledge.TypeConcept {
		return true
	}
	title := strings.TrimSpace(detail.Title)
	core := strings.TrimSpace(detail.CoreContent)
	if len(detail.StructureFields) > 0 {
		return true
	}
	if strings.HasPrefix(core, "本页定义") || strings.HasPrefix(core, "本文定义") || strings.HasPrefix(core, "本条目定义") {
		return false
	}
	return !singleCJKRune(title)
}

func singleCJKRune(value string) bool {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) != 1 {
		return false
	}
	r := runes[0]
	return (r >= 0x4E00 && r <= 0x9FFF) || (r >= 0x3400 && r <= 0x4DBF)
}

func frontmatterRelations(raw any) []knowledge.StructuredRelation {
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]knowledge.StructuredRelation, 0, len(items))
	for _, item := range items {
		values, ok := item.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, knowledge.StructuredRelation{
			RelationID:       frontmatterString(values, "relation_id"),
			RelationType:     frontmatterString(values, "relation_type"),
			TargetObjectID:   frontmatterString(values, "target_object_id"),
			TargetWikiPageID: frontmatterString(values, "target_wiki_page_id"),
			TargetTitle:      frontmatterString(values, "target_title"),
			TargetSlug:       frontmatterString(values, "target_slug"),
			EvidenceIDs:      frontmatterStringSlice(values["evidence_ids"]),
			Confidence:       frontmatterFloat(values, "confidence"),
		})
	}
	return out
}

func enrichStructuredRelationTargets(relations []knowledge.StructuredRelation, pages map[string]weknora.WikiPage) {
	if len(relations) == 0 || len(pages) == 0 {
		return
	}
	for index := range relations {
		relation := &relations[index]
		if relation.TargetWikiPageID == "" {
			continue
		}
		page, ok := pages[relation.TargetWikiPageID]
		if !ok {
			continue
		}
		if relation.TargetTitle == "" {
			relation.TargetTitle = firstNonEmpty(page.Title, firstMarkdownHeading(page.Content), page.Slug)
		}
		if relation.TargetSlug == "" {
			relation.TargetSlug = page.Slug
		}
	}
}

func frontmatterStringSlice(raw any) []string {
	switch values := raw.(type) {
	case []any:
		out := make([]string, 0, len(values))
		for _, value := range values {
			if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
				out = append(out, strings.TrimSpace(text))
			}
		}
		return out
	case []string:
		return append([]string(nil), values...)
	case string:
		return splitEvidenceIDs(values)
	default:
		return nil
	}
}

func frontmatterFloat(values map[string]any, key string) float64 {
	value, ok := values[key].(float64)
	if ok {
		return value
	}
	if integer, ok := values[key].(int); ok {
		return float64(integer)
	}
	return 0
}
