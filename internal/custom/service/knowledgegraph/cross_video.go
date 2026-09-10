package knowledgegraph

import (
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/custom/service/knowledge"
	"github.com/google/uuid"
)

const (
	CrossVideoStatusReady            = "ready"
	CrossVideoStatusEmpty            = "empty"
	CrossVideoStatusNotGenerated     = "not_generated"
	CrossVideoStatusCandidatePending = "candidate_pending"
	CrossVideoStatusFilterEmpty      = "filter_empty"
)

// CrossVideoVideo is the minimum video metadata needed to validate an
// association. The caller owns loading it from the business database.
type CrossVideoVideo struct {
	ID                   string
	Title                string
	VideoType            string
	TranscriptGeneration string
	DurationSeconds      int
}

// CrossVideoEvidence is a current-generation transcript anchor. Text is
// optional for callers that only need a seekable location.
type CrossVideoEvidence struct {
	ID                   string
	KnowledgeID          string
	VideoID              string
	TranscriptGeneration string
	Text                 string
	StartMs              int
	EndMs                int
}

// CrossVideoPage is a validated Wiki object projection. KnowledgeObjectID is
// the only identity used to discover a shared object across videos.
type CrossVideoPage struct {
	ID                   string
	KnowledgeObjectID    string
	Title                string
	KnowledgeType        knowledge.KnowledgeType
	VideoID              string
	TranscriptGeneration string
	AuditStatus          string
	EvidenceIDs          []string
}

type CrossVideoAssociation struct {
	ID                  string
	SourceWikiPageID    string
	TargetWikiPageID    string
	KnowledgeObjectID   string
	RelationType        string
	RelationKind        string
	RelationSource      string
	RelationDescription string
	SourceVideoID       string
	SourceVideoTitle    string
	SourceVideoType     string
	TargetVideoID       string
	TargetVideoTitle    string
	TargetVideoType     string
	SourceEvidence      CrossVideoEvidence
	TargetEvidence      CrossVideoEvidence
}

type CrossVideoRejected struct {
	SourceWikiPageID string `json:"source_wiki_page_id,omitempty"`
	TargetWikiPageID string `json:"target_wiki_page_id,omitempty"`
	Reason           string `json:"reason"`
}

type CrossVideoResult struct {
	Status           string
	Associations     []CrossVideoAssociation
	Rejected         []CrossVideoRejected
	CandidateCount   int
	CurrentPageCount int
	OtherVideoCount  int
}

// BuildCrossVideoAssociations creates deterministic, read-only associations
// from explicit shared object IDs. It deliberately does not compare titles,
// slugs, body text, or embeddings. Every returned association has a valid
// page pair, active generation metadata, and a seekable evidence anchor for
// each video.
func BuildCrossVideoAssociations(currentVideoID string, pages []CrossVideoPage, videos map[string]CrossVideoVideo, evidence map[string][]CrossVideoEvidence) CrossVideoResult {
	currentVideoID = strings.TrimSpace(currentVideoID)
	result := CrossVideoResult{
		Associations: make([]CrossVideoAssociation, 0),
		Rejected:     make([]CrossVideoRejected, 0),
	}
	if currentVideoID == "" {
		result.Status = CrossVideoStatusEmpty
		return result
	}
	currentVideo, currentOK := videos[currentVideoID]
	if !currentOK || strings.TrimSpace(currentVideo.TranscriptGeneration) == "" {
		result.Status = CrossVideoStatusEmpty
		return result
	}

	validPages := make([]CrossVideoPage, 0, len(pages))
	groups := make(map[string][]CrossVideoPage)
	for _, page := range pages {
		page.ID = strings.TrimSpace(page.ID)
		page.KnowledgeObjectID = strings.TrimSpace(page.KnowledgeObjectID)
		page.VideoID = strings.TrimSpace(page.VideoID)
		page.TranscriptGeneration = strings.TrimSpace(page.TranscriptGeneration)
		if _, err := uuid.Parse(page.ID); err != nil {
			result.Rejected = append(result.Rejected, CrossVideoRejected{SourceWikiPageID: page.ID, Reason: "wiki_page_id_invalid"})
			continue
		}
		if page.KnowledgeObjectID == "" || page.VideoID == "" || page.TranscriptGeneration == "" {
			result.Rejected = append(result.Rejected, CrossVideoRejected{SourceWikiPageID: page.ID, Reason: "page_identity_incomplete"})
			continue
		}
		video, ok := videos[page.VideoID]
		if !ok || strings.TrimSpace(video.TranscriptGeneration) == "" {
			result.Rejected = append(result.Rejected, CrossVideoRejected{SourceWikiPageID: page.ID, Reason: "source_video_missing"})
			continue
		}
		if page.TranscriptGeneration != video.TranscriptGeneration {
			result.Rejected = append(result.Rejected, CrossVideoRejected{SourceWikiPageID: page.ID, Reason: "transcript_generation_mismatch"})
			continue
		}
		if strings.ToLower(strings.TrimSpace(page.AuditStatus)) != "passed" || !knowledge.IsKnowledgeType(page.KnowledgeType) {
			result.Rejected = append(result.Rejected, CrossVideoRejected{SourceWikiPageID: page.ID, Reason: "page_not_audited"})
			continue
		}
		validPages = append(validPages, page)
		groups[page.KnowledgeObjectID] = append(groups[page.KnowledgeObjectID], page)
	}
	for _, page := range validPages {
		if page.VideoID == currentVideoID && page.TranscriptGeneration == currentVideo.TranscriptGeneration {
			result.CurrentPageCount++
		}
	}
	if result.CurrentPageCount == 0 {
		result.Status = CrossVideoStatusEmpty
		return result
	}

	seenVideos := make(map[string]struct{})
	seen := make(map[string]struct{})
	for _, source := range validPages {
		if source.VideoID != currentVideoID || source.TranscriptGeneration != currentVideo.TranscriptGeneration {
			continue
		}
		for _, target := range groups[source.KnowledgeObjectID] {
			if target.VideoID == currentVideoID {
				continue
			}
			result.CandidateCount++
			if target.KnowledgeType != source.KnowledgeType {
				result.Rejected = append(result.Rejected, CrossVideoRejected{SourceWikiPageID: source.ID, TargetWikiPageID: target.ID, Reason: "knowledge_type_mismatch"})
				continue
			}
			targetVideo, targetOK := videos[target.VideoID]
			if !targetOK {
				result.Rejected = append(result.Rejected, CrossVideoRejected{SourceWikiPageID: source.ID, TargetWikiPageID: target.ID, Reason: "target_video_missing"})
				continue
			}
			sourceEvidence, sourceOK := firstCurrentEvidence(source, videos[source.VideoID], evidence[source.VideoID])
			targetEvidence, targetEvidenceOK := firstCurrentEvidence(target, targetVideo, evidence[target.VideoID])
			if !sourceOK || !targetEvidenceOK {
				reason := "evidence_invalid"
				if sourceOK && !targetEvidenceOK {
					reason = "target_evidence_invalid"
				}
				result.Rejected = append(result.Rejected, CrossVideoRejected{SourceWikiPageID: source.ID, TargetWikiPageID: target.ID, Reason: reason})
				continue
			}
			key := source.ID + "\x00" + target.ID + "\x00" + sourceEvidence.ID + "\x00" + targetEvidence.ID
			if _, duplicate := seen[key]; duplicate {
				continue
			}
			seen[key] = struct{}{}
			seenVideos[targetVideo.ID] = struct{}{}
			result.Associations = append(result.Associations, CrossVideoAssociation{
				ID:               strings.Join([]string{source.ID, "shared_object", target.ID, target.VideoID, target.TranscriptGeneration}, ":"),
				SourceWikiPageID: source.ID, TargetWikiPageID: target.ID, KnowledgeObjectID: source.KnowledgeObjectID,
				RelationType: "shared_object", RelationKind: "cross_video", RelationSource: "shared_object",
				RelationDescription: "同一知识对象在其他视频中出现",
				SourceVideoID:       source.VideoID, SourceVideoTitle: videos[source.VideoID].Title, SourceVideoType: videos[source.VideoID].VideoType,
				TargetVideoID: target.VideoID, TargetVideoTitle: targetVideo.Title, TargetVideoType: targetVideo.VideoType,
				SourceEvidence: sourceEvidence, TargetEvidence: targetEvidence,
			})
		}
	}
	result.OtherVideoCount = len(seenVideos)
	sort.SliceStable(result.Associations, func(i, j int) bool {
		if result.Associations[i].TargetVideoID != result.Associations[j].TargetVideoID {
			return result.Associations[i].TargetVideoID < result.Associations[j].TargetVideoID
		}
		if result.Associations[i].SourceWikiPageID != result.Associations[j].SourceWikiPageID {
			return result.Associations[i].SourceWikiPageID < result.Associations[j].SourceWikiPageID
		}
		return result.Associations[i].TargetWikiPageID < result.Associations[j].TargetWikiPageID
	})
	if len(result.Associations) > 0 {
		result.Status = CrossVideoStatusReady
	} else if result.CandidateCount > 0 {
		result.Status = CrossVideoStatusFilterEmpty
	} else {
		result.Status = CrossVideoStatusCandidatePending
	}
	return result
}

func firstCurrentEvidence(page CrossVideoPage, video CrossVideoVideo, candidates []CrossVideoEvidence) (CrossVideoEvidence, bool) {
	byID := make(map[string]CrossVideoEvidence, len(candidates))
	for _, item := range candidates {
		if item.VideoID != video.ID || item.TranscriptGeneration != video.TranscriptGeneration || item.StartMs < 0 || item.EndMs <= item.StartMs {
			continue
		}
		if video.DurationSeconds > 0 && item.EndMs > video.DurationSeconds*1000 {
			continue
		}
		if strings.TrimSpace(item.ID) != "" {
			byID[item.ID] = item
		}
		if strings.TrimSpace(item.KnowledgeID) != "" {
			byID[item.KnowledgeID] = item
		}
	}
	ids := append([]string(nil), page.EvidenceIDs...)
	sort.Strings(ids)
	for _, id := range ids {
		if item, ok := byID[strings.TrimSpace(id)]; ok {
			return item, true
		}
	}
	return CrossVideoEvidence{}, false
}
