package handler

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
	"github.com/Tencent/WeKnora/internal/custom/model"
	"github.com/Tencent/WeKnora/internal/custom/service/knowledgegraph"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type crossVideoEvidenceResponse struct {
	ID                   string `json:"id"`
	KnowledgeID          string `json:"knowledge_id"`
	VideoID              string `json:"video_id"`
	VideoTitle           string `json:"video_title"`
	TranscriptGeneration string `json:"transcript_generation"`
	Text                 string `json:"text,omitempty"`
	StartMs              int    `json:"start_ms"`
	EndMs                int    `json:"end_ms"`
	Seconds              int    `json:"seconds"`
}

type crossVideoAssociationResponse struct {
	ID                  string                     `json:"id"`
	SourceWikiPageID    string                     `json:"source_wiki_page_id"`
	TargetWikiPageID    string                     `json:"target_wiki_page_id"`
	KnowledgeObjectID   string                     `json:"knowledge_object_id"`
	RelationType        string                     `json:"relation_type"`
	RelationKind        string                     `json:"relation_kind"`
	RelationSource      string                     `json:"relation_source"`
	RelationDescription string                     `json:"relation_description"`
	SourceVideoID       string                     `json:"source_video_id"`
	SourceVideoTitle    string                     `json:"source_video_title"`
	SourceVideoType     string                     `json:"source_video_type,omitempty"`
	TargetVideoID       string                     `json:"target_video_id"`
	TargetVideoTitle    string                     `json:"target_video_title"`
	TargetVideoType     string                     `json:"target_video_type,omitempty"`
	SourceEvidence      crossVideoEvidenceResponse `json:"source_evidence"`
	TargetEvidence      crossVideoEvidenceResponse `json:"target_evidence"`
}

type crossVideoResponse struct {
	Status           string                              `json:"status"`
	VideoID          string                              `json:"video_id"`
	WikiPageID       string                              `json:"wiki_page_id,omitempty"`
	Associations     []crossVideoAssociationResponse     `json:"associations"`
	Rejected         []knowledgegraph.CrossVideoRejected `json:"rejected"`
	CandidateCount   int                                 `json:"candidate_count"`
	CurrentPageCount int                                 `json:"current_page_count"`
	OtherVideoCount  int                                 `json:"other_video_count"`
}

// CrossVideo serves the read-only association contract for Graph and the
// video-detail page. A source page can be selected with wiki_page_id; without
// it all current-video objects are considered.
func (h *EntityGraphHandler) CrossVideo(c *gin.Context) {
	videoID := strings.TrimSpace(c.Query("video_id"))
	if videoID == "" {
		videoID = strings.TrimSpace(c.Query("source_video_id"))
	}
	if videoID == "" {
		graphFailure(c, http.StatusBadRequest, "video_id_required", "video_id is required", "")
		return
	}
	if h.db == nil || h.wiki == nil || h.evidence == nil || strings.TrimSpace(h.kbID) == "" {
		graphFailure(c, http.StatusServiceUnavailable, graphErrorReadFailed, "cross-video graph is unavailable", "")
		return
	}
	var current model.Video
	if err := h.db.WithContext(c.Request.Context()).First(&current, "id = ?", videoID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			graphFailure(c, http.StatusNotFound, "video_missing", "video not found", "")
			return
		}
		graphFailure(c, http.StatusBadGateway, graphErrorReadFailed, err.Error(), "")
		return
	}
	requestedPage := strings.TrimSpace(c.Query("wiki_page_id"))
	if requestedPage != "" {
		if _, err := uuid.Parse(requestedPage); err != nil {
			graphFailure(c, http.StatusBadRequest, graphErrorInvalidPageID, "wiki_page_id must be a UUID", requestedPage)
			return
		}
	}
	if strings.TrimSpace(current.KnowledgeBaseWikiPageID) == "" {
		c.JSON(http.StatusOK, gin.H{"success": true, "data": crossVideoResponse{
			Status: knowledgegraph.CrossVideoStatusNotGenerated, VideoID: videoID,
			Associations: []crossVideoAssociationResponse{}, Rejected: []knowledgegraph.CrossVideoRejected{},
		}})
		return
	}
	pages, err := h.wiki.ListAllPages(c.Request.Context(), h.kbID, "")
	if err != nil {
		graphFailure(c, http.StatusBadGateway, graphErrorReadFailed, err.Error(), "")
		return
	}
	videoIDs := make(map[string]struct{})
	crossPages := make([]knowledgegraph.CrossVideoPage, 0, len(pages))
	for _, page := range pages {
		detail := graphKnowledgeDetail(page)
		if detail == nil {
			continue
		}
		frontmatter := page.ParsedFrontmatter()
		pageVideoID := strings.TrimSpace(frontmatterString(frontmatter, "source_video_id"))
		if pageVideoID == "" || strings.TrimSpace(detail.KnowledgeObjectID) == "" {
			continue
		}
		if requestedPage != "" && pageVideoID == videoID && page.ID != requestedPage {
			continue
		}
		videoIDs[pageVideoID] = struct{}{}
		crossPages = append(crossPages, knowledgegraph.CrossVideoPage{
			ID: page.ID, KnowledgeObjectID: detail.KnowledgeObjectID, Title: detail.Title,
			KnowledgeType: detail.KnowledgeType, VideoID: pageVideoID,
			TranscriptGeneration: detail.TranscriptGeneration, AuditStatus: detail.AuditStatus,
			EvidenceIDs: append([]string(nil), detail.EvidenceIDs...),
		})
	}
	if requestedPage != "" {
		found := false
		for _, page := range crossPages {
			if page.ID == requestedPage && page.VideoID == videoID {
				found = true
				break
			}
		}
		if !found {
			graphFailure(c, http.StatusNotFound, graphErrorPageMissing, "wiki page not found for video", requestedPage)
			return
		}
	}
	if _, ok := videoIDs[videoID]; !ok {
		videoIDs[videoID] = struct{}{}
	}
	videos, err := h.loadCrossVideoVideos(c, videoIDs)
	if err != nil {
		graphFailure(c, http.StatusBadGateway, graphErrorReadFailed, err.Error(), "")
		return
	}
	evidence, err := h.loadCrossVideoEvidence(c, videos, crossPages)
	if err != nil {
		graphFailure(c, http.StatusBadGateway, graphErrorReadFailed, err.Error(), "")
		return
	}
	contexts := make(map[string]knowledgegraph.CrossVideoVideo, len(videos))
	for id, video := range videos {
		contexts[id] = knowledgegraph.CrossVideoVideo{ID: video.ID, Title: video.Title, VideoType: video.VideoType, TranscriptGeneration: video.TranscriptGeneration, DurationSeconds: video.DurationSeconds}
	}
	result := knowledgegraph.BuildCrossVideoAssociations(videoID, crossPages, contexts, evidence)
	response := crossVideoResponse{Status: result.Status, VideoID: videoID, WikiPageID: requestedPage, Associations: make([]crossVideoAssociationResponse, 0, len(result.Associations)), Rejected: result.Rejected, CandidateCount: result.CandidateCount, CurrentPageCount: result.CurrentPageCount, OtherVideoCount: result.OtherVideoCount}
	for _, association := range result.Associations {
		response.Associations = append(response.Associations, crossVideoAssociationResponse{
			ID: association.ID, SourceWikiPageID: association.SourceWikiPageID, TargetWikiPageID: association.TargetWikiPageID,
			KnowledgeObjectID: association.KnowledgeObjectID, RelationType: association.RelationType, RelationKind: association.RelationKind,
			RelationSource: association.RelationSource, RelationDescription: association.RelationDescription,
			SourceVideoID: association.SourceVideoID, SourceVideoTitle: association.SourceVideoTitle, SourceVideoType: association.SourceVideoType,
			TargetVideoID: association.TargetVideoID, TargetVideoTitle: association.TargetVideoTitle, TargetVideoType: association.TargetVideoType,
			SourceEvidence: h.crossVideoEvidenceResponse(association.SourceEvidence, videos), TargetEvidence: h.crossVideoEvidenceResponse(association.TargetEvidence, videos),
		})
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": response})
}

func (h *EntityGraphHandler) loadCrossVideoVideos(c *gin.Context, ids map[string]struct{}) (map[string]model.Video, error) {
	values := make([]string, 0, len(ids))
	for id := range ids {
		values = append(values, id)
	}
	sort.Strings(values)
	var rows []model.Video
	if err := h.db.WithContext(c.Request.Context()).Where("id IN ?", values).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("load cross-video videos: %w", err)
	}
	out := make(map[string]model.Video, len(rows))
	for _, row := range rows {
		out[row.ID] = row
	}
	return out, nil
}

func (h *EntityGraphHandler) loadCrossVideoEvidence(c *gin.Context, videos map[string]model.Video, pages []knowledgegraph.CrossVideoPage) (map[string][]knowledgegraph.CrossVideoEvidence, error) {
	out := make(map[string][]knowledgegraph.CrossVideoEvidence, len(videos))
	for videoID, video := range videos {
		if strings.TrimSpace(video.TranscriptGeneration) == "" {
			continue
		}
		var chunks []model.VideoTranscriptChunk
		if err := h.db.WithContext(c.Request.Context()).Where("video_id = ? AND generation = ? AND status = ?", videoID, video.TranscriptGeneration, "completed").Order("chunk_index ASC").Find(&chunks).Error; err != nil {
			return nil, fmt.Errorf("load cross-video evidence %s: %w", videoID, err)
		}
		required := make(map[string]struct{})
		for _, page := range pages {
			if page.VideoID != videoID {
				continue
			}
			for _, evidenceID := range page.EvidenceIDs {
				required[strings.TrimSpace(evidenceID)] = struct{}{}
			}
		}
		for _, chunk := range chunks {
			if len(required) > 0 {
				_, byKnowledgeID := required[chunk.KnowledgeID]
				_, bySentenceID := required[chunk.EvidenceSentenceID]
				if !byKnowledgeID && !bySentenceID {
					continue
				}
			}
			text, err := h.readCrossVideoEvidenceText(c, chunk.KnowledgeID)
			if err != nil {
				return nil, err
			}
			item := knowledgegraph.CrossVideoEvidence{ID: firstNonEmpty(chunk.EvidenceSentenceID, chunk.KnowledgeID), KnowledgeID: chunk.KnowledgeID, VideoID: video.ID, TranscriptGeneration: chunk.Generation, Text: text, StartMs: chunk.StartMs, EndMs: chunk.EndMs}
			out[videoID] = append(out[videoID], item)
		}
	}
	return out, nil
}

func (h *EntityGraphHandler) readCrossVideoEvidenceText(c *gin.Context, knowledgeID string) (string, error) {
	chunks, err := h.evidence.ListKnowledgeChunks(c.Request.Context(), knowledgeID)
	if err != nil {
		return "", fmt.Errorf("read evidence %q: %w", knowledgeID, err)
	}
	content, err := weknora.JoinKnowledgeChunks(chunks)
	if err != nil {
		return "", fmt.Errorf("read evidence %q: %w", knowledgeID, err)
	}
	return graphEvidenceText(content), nil
}

func (h *EntityGraphHandler) crossVideoEvidenceResponse(item knowledgegraph.CrossVideoEvidence, videos map[string]model.Video) crossVideoEvidenceResponse {
	return crossVideoEvidenceResponse{ID: item.ID, KnowledgeID: item.KnowledgeID, VideoID: item.VideoID, VideoTitle: videos[item.VideoID].Title, TranscriptGeneration: item.TranscriptGeneration, Text: item.Text, StartMs: item.StartMs, EndMs: item.EndMs, Seconds: item.StartMs / 1000}
}
