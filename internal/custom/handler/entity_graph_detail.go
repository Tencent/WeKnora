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
	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	graphStatusReady        = "ready"
	graphStatusPartial      = "partial"
	graphStatusEmpty        = "empty"
	graphStatusFilterEmpty  = "filter_empty"
	graphStatusNotGenerated = "not_generated"
	graphStatusNotProjected = "not_projected"
	graphStatusVideoMissing = "video_missing"

	graphErrorInvalidPageID      = "invalid_wiki_page_id"
	graphErrorUnknownFilter      = "unknown_filter"
	graphErrorPageMissing        = "wiki_page_missing"
	graphErrorPageInvalid        = "wiki_page_invalid"
	graphErrorUnknownType        = "unknown_knowledge_type"
	graphErrorSourceMissing      = "source_video_missing"
	graphErrorGenerationMismatch = "transcript_generation_mismatch"
	graphErrorEvidenceInvalid    = "evidence_invalid"
	graphErrorReadFailed         = "graph_read_failed"
)

type entityGraphDetailCounts struct {
	FormalRelations               int `json:"formal_relations"`
	FormalRelationDenominator     int `json:"formal_relation_denominator"`
	ReadingAssociations           int `json:"reading_associations"`
	ReadingAssociationDenominator int `json:"reading_association_denominator"`
	Evidence                      int `json:"evidence"`
	EvidenceDenominator           int `json:"evidence_denominator"`
}

type entityGraphDetailResponse struct {
	Status              string                          `json:"status"`
	KnowledgeBaseID     string                          `json:"knowledge_base_id"`
	Detail              EntityGraphKnowledgeDetail      `json:"detail"`
	Evidence            []EntityGraphEvidence           `json:"evidence"`
	FormalRelations     []EntityGraphEdge               `json:"formal_relations"`
	ReadingAssociations []EntityGraphReadingAssociation `json:"reading_associations"`
	Counts              entityGraphDetailCounts         `json:"counts"`
}

func (h *EntityGraphHandler) Detail(c *gin.Context) {
	pageID := strings.TrimSpace(c.Param("wikiPageID"))
	if _, err := uuid.Parse(pageID); err != nil {
		graphFailure(c, http.StatusBadRequest, graphErrorInvalidPageID, "wiki_page_id must be a UUID", pageID)
		return
	}
	if h.wiki == nil || h.evidence == nil || h.graph == nil || strings.TrimSpace(h.kbID) == "" || h.db == nil {
		graphFailure(c, http.StatusServiceUnavailable, graphErrorReadFailed, "Wiki graph detail is unavailable", pageID)
		return
	}
	page, err := h.wiki.GetPageByID(c.Request.Context(), h.kbID, pageID)
	if err != nil {
		graphFailure(c, http.StatusBadGateway, graphErrorReadFailed, err.Error(), pageID)
		return
	}
	if page == nil || strings.TrimSpace(page.ID) == "" {
		graphFailure(c, http.StatusNotFound, graphErrorPageMissing, "wiki page not found", pageID)
		return
	}
	if page.ID != pageID {
		graphFailure(c, http.StatusUnprocessableEntity, graphErrorPageInvalid, "wiki page identity does not match the requested page ID", pageID)
		return
	}
	detail := graphKnowledgeDetail(*page)
	if detail == nil {
		rawType := rawGraphKnowledgeType(page.ParsedFrontmatter(), page.PageType)
		graphFailure(c, http.StatusUnprocessableEntity, graphErrorUnknownType, fmt.Sprintf("unsupported Wiki knowledge type %q", rawType), pageID)
		return
	}
	if strings.TrimSpace(detail.KnowledgeObjectID) == "" ||
		strings.TrimSpace(frontmatterString(page.ParsedFrontmatter(), "source_video_id")) == "" ||
		strings.TrimSpace(detail.TranscriptGeneration) == "" || strings.ToLower(strings.TrimSpace(detail.AuditStatus)) != "passed" ||
		strings.TrimSpace(detail.CoreContent) == "" {
		graphFailure(c, http.StatusUnprocessableEntity, graphErrorPageInvalid, "wiki page does not satisfy the knowledge detail contract", pageID)
		return
	}

	frontmatter := page.ParsedFrontmatter()
	videoID := frontmatterString(frontmatter, "source_video_id")
	var video model.Video
	if err := h.db.WithContext(c.Request.Context()).First(&video, "id = ?", videoID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			graphFailure(c, http.StatusUnprocessableEntity, graphErrorSourceMissing, "source video not found", pageID)
			return
		}
		graphFailure(c, http.StatusBadGateway, graphErrorReadFailed, "load source video: "+err.Error(), pageID)
		return
	}
	if strings.TrimSpace(video.TranscriptGeneration) == "" || detail.TranscriptGeneration != video.TranscriptGeneration {
		graphFailure(c, http.StatusConflict, graphErrorGenerationMismatch, "Wiki page does not belong to the current transcript generation", pageID)
		return
	}

	evidence, chunkByEvidence, chunkByIndex, err := h.detailEvidence(c.Request.Context(), video, detail)
	if err != nil {
		graphFailure(c, http.StatusUnprocessableEntity, graphErrorEvidenceInvalid, err.Error(), pageID)
		return
	}
	detail.VideoID = video.ID
	detail.VideoTitle = video.Title
	detail.SourceVideoTitle = video.Title
	if len(evidence) > 0 {
		detail.Seconds = evidence[0].StartMs / 1000
		detail.Timestamp = formatGraphTimestamp(detail.Seconds)
	}

	formalRelations := make([]EntityGraphEdge, 0)
	adjacentEdges := make([]knowledgegraph.Edge, 0)
	formalRelationDenominator := 0
	status := graphStatusNotProjected
	graph, queryErr := h.graph.Query(c.Request.Context(), knowledgegraph.Query{VideoID: video.ID, WikiPageID: pageID, Limit: 1})
	if queryErr != nil {
		graphFailure(c, http.StatusBadGateway, graphErrorReadFailed, queryErr.Error(), pageID)
		return
	}
	if graph != nil {
		adjacentEdges = graph.Edges
		for _, edge := range graph.Edges {
			if !weakWikiLinkEdge(edge) {
				formalRelationDenominator++
			}
		}
		for _, node := range graph.Nodes {
			if node.WikiPageID == pageID {
				status = graphStatusReady
				break
			}
		}
	}

	pages, err := h.wiki.ListAllPages(c.Request.Context(), h.kbID, "")
	if err != nil {
		graphFailure(c, http.StatusBadGateway, graphErrorReadFailed, err.Error(), pageID)
		return
	}
	pageByID := make(map[string]weknora.WikiPage, len(pages))
	for _, candidate := range pages {
		pageByID[candidate.ID] = candidate
	}
	pageByID[page.ID] = *page
	for _, edge := range adjacentEdges {
		for _, endpointID := range []string{edge.SourceWikiPageID, edge.TargetWikiPageID} {
			if _, parseErr := uuid.Parse(endpointID); parseErr != nil {
				continue
			}
			candidate, exists := pageByID[endpointID]
			if exists && strings.TrimSpace(candidate.Content) != "" {
				continue
			}
			loaded, readErr := h.wiki.GetPageByID(c.Request.Context(), h.kbID, endpointID)
			if readErr != nil {
				graphFailure(c, http.StatusBadGateway, graphErrorReadFailed, readErr.Error(), pageID)
				return
			}
			if loaded != nil && loaded.ID == endpointID {
				pageByID[endpointID] = *loaded
			}
		}
	}
	formalRelations = formalRelationsForPage(
		pageID,
		adjacentEdges,
		pageByID,
		video.ID,
		video.TranscriptGeneration,
		chunkByEvidence,
		chunkByIndex,
	)
	detail.Relations = structuredRelationsFromDetailEdges(pageID, formalRelations, pageByID)
	if status == graphStatusReady && len(formalRelations) != formalRelationDenominator {
		status = graphStatusPartial
	}
	reading := buildReadingAssociations([]EntityGraphNode{{WikiPageID: pageID}}, pageByID)
	response := entityGraphDetailResponse{
		Status: status, KnowledgeBaseID: h.kbID, Detail: *detail,
		Evidence: evidence, FormalRelations: formalRelations, ReadingAssociations: reading,
	}
	response.Counts = entityGraphDetailCounts{
		FormalRelations: len(formalRelations), FormalRelationDenominator: formalRelationDenominator,
		ReadingAssociations: len(reading), ReadingAssociationDenominator: 1,
		Evidence: len(evidence), EvidenceDenominator: len(detail.EvidenceIDs),
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": response})
}

func (h *EntityGraphHandler) detailEvidence(ctx context.Context, video model.Video, detail *EntityGraphKnowledgeDetail) ([]EntityGraphEvidence, map[string]model.VideoTranscriptChunk, map[string]model.VideoTranscriptChunk, error) {
	if detail == nil || len(detail.EvidenceIDs) == 0 {
		return nil, nil, nil, fmt.Errorf("wiki page has no evidence IDs")
	}
	var chunks []model.VideoTranscriptChunk
	if err := h.db.WithContext(ctx).
		Where("video_id = ? AND generation = ? AND status = ?", video.ID, video.TranscriptGeneration, "completed").
		Order("chunk_index ASC").Find(&chunks).Error; err != nil {
		return nil, nil, nil, fmt.Errorf("load evidence manifest: %w", err)
	}
	byEvidence := make(map[string]model.VideoTranscriptChunk, len(chunks)*2)
	byIndex := make(map[string]model.VideoTranscriptChunk, len(chunks))
	for _, chunk := range chunks {
		prefix := chunk.VideoID + "\x00" + chunk.Generation + "\x00"
		byEvidence[prefix+chunk.KnowledgeID] = chunk
		if chunk.EvidenceSentenceID != "" {
			byEvidence[prefix+chunk.EvidenceSentenceID] = chunk
		}
		byIndex[prefix+strconv.Itoa(chunk.ChunkIndex)] = chunk
	}
	result := make([]EntityGraphEvidence, 0, len(detail.EvidenceIDs))
	for _, evidenceID := range detail.EvidenceIDs {
		chunk, ok := resolveEvidenceChunk(byEvidence, byIndex, video.ID, video.TranscriptGeneration, evidenceID)
		if !ok {
			return nil, nil, nil, fmt.Errorf("evidence %q is missing from the current transcript generation", evidenceID)
		}
		text := ""
		if h.evidence != nil {
			chunks, readErr := h.evidence.ListKnowledgeChunks(ctx, chunk.KnowledgeID)
			if readErr != nil {
				return nil, nil, nil, fmt.Errorf("read evidence %q: %w", evidenceID, readErr)
			}
			content, joinErr := weknora.JoinKnowledgeChunks(chunks)
			if joinErr != nil {
				return nil, nil, nil, fmt.Errorf("read evidence %q: %w", evidenceID, joinErr)
			}
			text = graphEvidenceText(content)
			if text == "" {
				return nil, nil, nil, fmt.Errorf("evidence %q has no source sentence", evidenceID)
			}
		}
		result = append(result, EntityGraphEvidence{
			VideoID: video.ID, VideoTitle: video.Title, TranscriptGeneration: video.TranscriptGeneration,
			EvidenceSentenceID: firstNonEmpty(chunk.EvidenceSentenceID, evidenceID), KnowledgeID: chunk.KnowledgeID,
			Text: text, StartMs: chunk.StartMs, EndMs: chunk.EndMs, Seconds: chunk.StartMs / 1000,
			ChunkIndex: chunk.ChunkIndex, ChunkIDs: []string{chunk.KnowledgeID},
		})
	}
	return result, byEvidence, byIndex, nil
}

func graphEvidenceText(content string) string {
	const marker = "## 原文"
	index := strings.Index(content, marker)
	if index < 0 {
		return strings.TrimSpace(content)
	}
	text := strings.TrimSpace(content[index+len(marker):])
	if summary := strings.Index(text, "\n# Summary"); summary >= 0 {
		text = strings.TrimSpace(text[:summary])
	}
	return text
}

func formalRelationsForPage(
	pageID string,
	edges []knowledgegraph.Edge,
	pages map[string]weknora.WikiPage,
	videoID string,
	generation string,
	byEvidence map[string]model.VideoTranscriptChunk,
	byIndex map[string]model.VideoTranscriptChunk,
) []EntityGraphEdge {
	result := make([]EntityGraphEdge, 0)
	for _, edge := range edges {
		if edge.SourceWikiPageID != pageID && edge.TargetWikiPageID != pageID {
			continue
		}
		if weakWikiLinkEdge(edge) || !knowledgegraph.IsFormalRelationType(edge.RelationType) {
			continue
		}
		if !graphEndpointCurrent(pages[edge.SourceWikiPageID], edge.SourceWikiPageID, videoID, generation, byEvidence, byIndex) ||
			!graphEndpointCurrent(pages[edge.TargetWikiPageID], edge.TargetWikiPageID, videoID, generation, byEvidence, byIndex) {
			continue
		}
		if !graphEvidenceIDsCurrent(edge.EvidenceIDs, videoID, generation, byEvidence, byIndex) {
			continue
		}
		sourceDetail := graphKnowledgeDetail(pages[edge.SourceWikiPageID])
		targetDetail := graphKnowledgeDetail(pages[edge.TargetWikiPageID])
		if sourceDetail == nil || targetDetail == nil {
			continue
		}
		result = append(result, EntityGraphEdge{
			ID: edge.ID, Source: "wiki:" + edge.SourceWikiPageID, Target: "wiki:" + edge.TargetWikiPageID,
			SourceTitle: sourceDetail.Title, SourceSlug: sourceDetail.Slug,
			TargetTitle: targetDetail.Title, TargetSlug: targetDetail.Slug,
			Type: edge.RelationType, Weight: 1, Confidence: edge.Confidence, EvidenceIDs: edge.EvidenceIDs,
			RelationKind: "semantic", RelationSource: "skill", Counted: true,
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func graphEndpointCurrent(
	page weknora.WikiPage,
	pageID string,
	videoID string,
	generation string,
	byEvidence map[string]model.VideoTranscriptChunk,
	byIndex map[string]model.VideoTranscriptChunk,
) bool {
	if _, err := uuid.Parse(pageID); err != nil || page.ID != pageID || strings.TrimSpace(page.Content) == "" {
		return false
	}
	detail := graphKnowledgeDetail(page)
	if detail == nil || detail.KnowledgeObjectID == "" || detail.TranscriptGeneration != generation ||
		strings.ToLower(strings.TrimSpace(detail.AuditStatus)) != "passed" || strings.TrimSpace(detail.CoreContent) == "" ||
		frontmatterString(page.ParsedFrontmatter(), "source_video_id") != videoID || !displayableGraphDetail(detail) {
		return false
	}
	return graphEvidenceIDsCurrent(detail.EvidenceIDs, videoID, generation, byEvidence, byIndex)
}

func graphEvidenceIDsCurrent(
	values []string,
	videoID string,
	generation string,
	byEvidence map[string]model.VideoTranscriptChunk,
	byIndex map[string]model.VideoTranscriptChunk,
) bool {
	if len(values) == 0 {
		return false
	}
	for _, value := range values {
		if _, ok := resolveEvidenceChunk(byEvidence, byIndex, videoID, generation, value); !ok {
			return false
		}
	}
	return true
}

func structuredRelationsFromDetailEdges(pageID string, edges []EntityGraphEdge, pages map[string]weknora.WikiPage) []knowledge.StructuredRelation {
	result := make([]knowledge.StructuredRelation, 0)
	for _, edge := range edges {
		if edge.Source != "wiki:"+pageID {
			continue
		}
		targetPageID := strings.TrimPrefix(edge.Target, "wiki:")
		targetPage, ok := pages[targetPageID]
		if !ok {
			continue
		}
		targetDetail := graphKnowledgeDetail(targetPage)
		if targetDetail == nil {
			continue
		}
		result = append(result, knowledge.StructuredRelation{
			RelationID: edge.ID, RelationType: edge.Type,
			TargetObjectID: targetDetail.KnowledgeObjectID, TargetWikiPageID: targetPageID,
			TargetTitle: targetDetail.Title, TargetSlug: targetDetail.Slug,
			EvidenceIDs: append([]string(nil), edge.EvidenceIDs...), Confidence: edge.Confidence,
		})
	}
	return result
}

func rawGraphKnowledgeType(frontmatter map[string]any, pageType string) string {
	primary := strings.ToLower(frontmatterString(frontmatter, "primary_type"))
	compatibility := strings.ToLower(frontmatterString(frontmatter, "type"))
	if primary != "" && compatibility != "" && primary != compatibility {
		return primary + "|" + compatibility
	}
	return firstNonEmpty(primary, compatibility, strings.ToLower(strings.TrimSpace(pageType)))
}

func graphFailure(c *gin.Context, httpStatus int, code, message, pageID string) {
	c.JSON(httpStatus, gin.H{
		"success": false, "status": "failed", "stage": "graph", "error_code": code,
		"error_message": message, "error": message, "wiki_page_id": pageID,
	})
}

func (h *EntityGraphHandler) overviewStatus(ctx context.Context, source *knowledgegraph.Graph, response *entityGraphResponse, videoID string, filtered bool) string {
	if response == nil {
		return graphStatusEmpty
	}
	scopeTotal := 0
	filteredTotal := 0
	if source != nil {
		scopeTotal = source.Stats.ScopeTotal
		filteredTotal = source.Stats.FilteredTotal
		if scopeTotal == 0 && len(source.Nodes) > 0 {
			scopeTotal = len(source.Nodes)
		}
		if filteredTotal == 0 && len(source.Nodes) > 0 {
			filteredTotal = len(source.Nodes)
		}
	}
	if scopeTotal > 0 {
		if filtered && filteredTotal == 0 {
			return graphStatusFilterEmpty
		}
		return response.Status
	}
	if videoID == "" {
		return graphStatusEmpty
	}
	var video model.Video
	if err := h.db.WithContext(ctx).Select("id", "knowledge_base_wiki_page_id").First(&video, "id = ?", videoID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return graphStatusVideoMissing
		}
		return graphStatusEmpty
	}
	if strings.TrimSpace(video.KnowledgeBaseWikiPageID) == "" {
		return graphStatusNotGenerated
	}
	return graphStatusNotProjected
}
