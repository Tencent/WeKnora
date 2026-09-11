package trainingorchestration

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/custom/service/summary"
)

const (
	PlanningContractVersion   = "training-orchestration/planning/v1"
	MaterialContractVersion   = "training-orchestration/material/v1"
	GenerationContractVersion = "training-orchestration/generation/v1"
	RelationContractVersion   = "training-orchestration/relation/v1"
)

// CatalogSnapshot is the bounded input planned for stage one. It contains
// routing cards and immutable references, never full summary or transcript text.
type CatalogSnapshot struct {
	ContractVersion   string         `json:"contract_version"`
	OwnerScopeID      string         `json:"owner_scope_id"`
	SourceFingerprint string         `json:"source_fingerprint"`
	Videos            []CatalogVideo `json:"videos"`
	SkippedVideos     []SkippedVideo `json:"skipped_videos"`
}

type CatalogVideo struct {
	VideoID              string                        `json:"video_id"`
	Title                string                        `json:"title"`
	VideoType            string                        `json:"video_type"`
	DurationSeconds      int                           `json:"duration_seconds"`
	TranscriptGeneration string                        `json:"transcript_generation"`
	SummaryWikiPageID    string                        `json:"summary_wiki_page_id,omitempty"`
	SummaryVersion       int                           `json:"summary_version,omitempty"`
	OrchestrationProfile *summary.OrchestrationProfile `json:"orchestration_profile,omitempty"`
	CompatibilityProfile *summary.OrchestrationProfile `json:"compatibility_profile,omitempty"`
}

type PlanDraft struct {
	ContractVersion   string            `json:"contract_version"`
	SourceFingerprint string            `json:"source_fingerprint"`
	TopicClusters     []PlanCluster     `json:"topic_clusters"`
	UnselectedVideos  []UnselectedVideo `json:"unselected_videos"`
}

type PlanCluster struct {
	ClusterKey        string            `json:"cluster_key"`
	Title             string            `json:"title"`
	Summary           string            `json:"summary"`
	LearningGoal      string            `json:"learning_goal"`
	PrimaryTemplate   string            `json:"primary_template"`
	Scope             string            `json:"scope"`
	InclusionCriteria []string          `json:"inclusion_criteria"`
	ExclusionCriteria []string          `json:"exclusion_criteria"`
	SourceVideoIDs    []string          `json:"source_video_ids"`
	UncertainVideoIDs []string          `json:"uncertain_video_ids"`
	MaterialRequests  []MaterialRequest `json:"material_requests"`
	Confidence        float64           `json:"confidence"`
	ReviewStatus      string            `json:"review_status"`
}

type MaterialRequest struct {
	VideoID              string   `json:"video_id"`
	SummaryWikiPageID    string   `json:"summary_wiki_page_id"`
	SummaryVersion       int      `json:"summary_version"`
	TranscriptGeneration string   `json:"transcript_generation"`
	SummaryBlockIDs      []string `json:"summary_block_ids"`
	EvidenceIDs          []string `json:"evidence_ids"`
}

type UnselectedVideo struct {
	VideoID string `json:"video_id"`
	Reason  string `json:"reason"`
}

type ClusterMaterial struct {
	ContractVersion string             `json:"contract_version"`
	ClusterKey      string             `json:"cluster_key"`
	SourceVideoIDs  []string           `json:"source_video_ids"`
	SummaryBlocks   []MaterialBlock    `json:"summary_blocks"`
	Evidence        []MaterialEvidence `json:"evidence"`
	// Retrieval metadata is runtime state, not model input. Keep it out of
	// the generation prompt so a warning can never be mistaken for evidence.
	RetrievalDegraded          bool   `json:"-"`
	RetrievalDegradationReason string `json:"-"`
}

type MaterialBlock struct {
	VideoID string `json:"video_id"`
	BlockID string `json:"block_id"`
	Text    string `json:"text"`
}

type MaterialEvidence struct {
	VideoID              string `json:"video_id"`
	TranscriptGeneration string `json:"transcript_generation"`
	EvidenceID           string `json:"evidence_id"`
	StartMs              int    `json:"start_ms"`
	EndMs                int    `json:"end_ms"`
	Text                 string `json:"text"`
}

const (
	PlanCandidate = "candidate"
	PlanAccepted  = "accepted"
	PlanAbstained = "abstained"
	PlanRejected  = "rejected"
)

func (p PlanDraft) ValidateAgainst(snapshot CatalogSnapshot) error {
	return p.validateAgainst(snapshot, false)
}

func isTemporaryClusterKey(clusterKey string) bool {
	return strings.HasPrefix(strings.TrimSpace(clusterKey), "candidate-")
}

func (p PlanDraft) validateAgainst(snapshot CatalogSnapshot, allowDuplicateTemporaryClusterKeys bool) error {
	if strings.TrimSpace(p.ContractVersion) != PlanningContractVersion {
		return fmt.Errorf("unsupported plan contract version %q", p.ContractVersion)
	}
	if strings.TrimSpace(snapshot.ContractVersion) != PlanningContractVersion {
		return fmt.Errorf("unsupported catalog contract version %q", snapshot.ContractVersion)
	}
	if strings.TrimSpace(p.SourceFingerprint) == "" || p.SourceFingerprint != snapshot.SourceFingerprint {
		return fmt.Errorf("plan source fingerprint does not match catalog snapshot")
	}
	videoSet := make(map[string]CatalogVideo, len(snapshot.Videos))
	for _, video := range snapshot.Videos {
		id := strings.TrimSpace(video.VideoID)
		if id == "" {
			return fmt.Errorf("catalog contains an empty video ID")
		}
		if _, duplicate := videoSet[id]; duplicate {
			return fmt.Errorf("catalog contains duplicate video ID %q", id)
		}
		videoSet[id] = video
	}
	seenClusterKeys := make(map[string]struct{}, len(p.TopicClusters))
	selectedVideos := make(map[string]struct{})
	for index, cluster := range p.TopicClusters {
		if strings.TrimSpace(cluster.ClusterKey) == "" || strings.TrimSpace(cluster.Title) == "" || strings.TrimSpace(cluster.LearningGoal) == "" {
			return fmt.Errorf("topic cluster %d identity or learning goal is empty", index+1)
		}
		if _, duplicate := seenClusterKeys[cluster.ClusterKey]; duplicate && !(allowDuplicateTemporaryClusterKeys && isTemporaryClusterKey(cluster.ClusterKey)) {
			return fmt.Errorf("topic cluster %d duplicates cluster key %q", index+1, cluster.ClusterKey)
		}
		seenClusterKeys[cluster.ClusterKey] = struct{}{}
		if _, ok := allowedTemplates[strings.TrimSpace(cluster.PrimaryTemplate)]; !ok {
			return fmt.Errorf("topic cluster %d has unsupported template %q", index+1, cluster.PrimaryTemplate)
		}
		if cluster.Confidence < 0 || cluster.Confidence > 1 {
			return fmt.Errorf("topic cluster %d has confidence outside [0,1]", index+1)
		}
		switch cluster.ReviewStatus {
		case PlanCandidate, PlanAccepted, PlanAbstained, PlanRejected:
		default:
			return fmt.Errorf("topic cluster %d has unsupported review status %q", index+1, cluster.ReviewStatus)
		}
		if (cluster.ReviewStatus == PlanAbstained || cluster.ReviewStatus == PlanRejected) && (len(cluster.SourceVideoIDs) > 0 || len(cluster.MaterialRequests) > 0) {
			return fmt.Errorf("topic cluster %d with review status %q cannot carry source videos or material requests", index+1, cluster.ReviewStatus)
		}
		if cluster.ReviewStatus != PlanAbstained && cluster.ReviewStatus != PlanRejected && len(cluster.SourceVideoIDs) == 0 {
			return fmt.Errorf("topic cluster %d has no source videos", index+1)
		}
		members := make(map[string]struct{}, len(cluster.SourceVideoIDs))
		for _, videoID := range cluster.SourceVideoIDs {
			videoID = strings.TrimSpace(videoID)
			if _, ok := videoSet[videoID]; !ok {
				return fmt.Errorf("topic cluster %d references video outside catalog: %s", index+1, videoID)
			}
			if _, duplicate := members[videoID]; duplicate {
				return fmt.Errorf("topic cluster %d repeats source video %s", index+1, videoID)
			}
			members[videoID] = struct{}{}
			selectedVideos[videoID] = struct{}{}
		}
		if cluster.ReviewStatus == PlanAbstained || cluster.ReviewStatus == PlanRejected {
			continue
		}
		if len(cluster.MaterialRequests) != len(members) {
			return fmt.Errorf("topic cluster %d must have exactly one material request per source video", index+1)
		}
		seenRequests := make(map[string]struct{}, len(cluster.MaterialRequests))
		for requestIndex, request := range cluster.MaterialRequests {
			requestVideoID := strings.TrimSpace(request.VideoID)
			video, ok := videoSet[requestVideoID]
			if !ok {
				return fmt.Errorf("topic cluster %d material request %d references video outside catalog", index+1, requestIndex+1)
			}
			if _, duplicate := seenRequests[requestVideoID]; duplicate {
				return fmt.Errorf("topic cluster %d repeats material request for video %s", index+1, requestVideoID)
			}
			seenRequests[requestVideoID] = struct{}{}
			if _, ok := members[requestVideoID]; !ok {
				return fmt.Errorf("topic cluster %d material request %d is outside cluster members", index+1, requestIndex+1)
			}
			if strings.TrimSpace(request.TranscriptGeneration) == "" {
				return fmt.Errorf("topic cluster %d material request %d has incomplete source identity", index+1, requestIndex+1)
			}
			if request.SummaryWikiPageID != video.SummaryWikiPageID || request.SummaryVersion != video.SummaryVersion || request.TranscriptGeneration != video.TranscriptGeneration {
				return fmt.Errorf("topic cluster %d material request %d does not match catalog version", index+1, requestIndex+1)
			}
			if len(request.EvidenceIDs) == 0 || (video.OrchestrationProfile != nil && len(request.SummaryBlockIDs) == 0) {
				return fmt.Errorf("topic cluster %d material request %d is empty", index+1, requestIndex+1)
			}
			profile, err := video.routingProfile()
			if err != nil {
				return fmt.Errorf("topic cluster %d material request %d: %w", index+1, requestIndex+1, err)
			}
			if profile == nil {
				return fmt.Errorf("topic cluster %d material request %d has no orchestration or compatibility profile whitelist", index+1, requestIndex+1)
			}
			allowedBlocks, allowedEvidence := orchestrationProfileReferences(profile)
			if err := validateRequestedIDs(request.SummaryBlockIDs, allowedBlocks, "summary block", index+1, requestIndex+1); err != nil {
				return err
			}
			if err := validateRequestedIDs(request.EvidenceIDs, allowedEvidence, "evidence", index+1, requestIndex+1); err != nil {
				return err
			}
		}
	}
	seenUnselected := make(map[string]struct{}, len(p.UnselectedVideos))
	for index, unselected := range p.UnselectedVideos {
		videoID := strings.TrimSpace(unselected.VideoID)
		if videoID == "" || strings.TrimSpace(unselected.Reason) == "" {
			return fmt.Errorf("unselected video %d requires video ID and reason", index+1)
		}
		if _, ok := videoSet[videoID]; !ok {
			return fmt.Errorf("unselected video %d references video outside catalog: %s", index+1, videoID)
		}
		if _, duplicate := seenUnselected[videoID]; duplicate {
			return fmt.Errorf("unselected videos repeat %s", videoID)
		}
		seenUnselected[videoID] = struct{}{}
		if _, selected := selectedVideos[videoID]; selected {
			return fmt.Errorf("video %s is both selected and unselected", videoID)
		}
	}
	for videoID := range videoSet {
		if _, selected := selectedVideos[videoID]; selected {
			continue
		}
		if _, unselected := seenUnselected[videoID]; !unselected {
			return fmt.Errorf("catalog video %s is neither selected nor explicitly unselected", videoID)
		}
	}
	return nil
}

// CatalogFingerprint returns a stable identity for the bounded stage-one
// source. Model and prompt versions are included by the caller so changing
// routing rules never reuses an older plan.
func CatalogFingerprint(snapshot CatalogSnapshot, modelName, promptVersion string) (string, error) {
	copySnapshot := canonicalCatalogSnapshot(snapshot)
	copySnapshot.SourceFingerprint = ""
	payload := struct {
		Snapshot      CatalogSnapshot `json:"snapshot"`
		Model         string          `json:"model"`
		PromptVersion string          `json:"prompt_version"`
	}{copySnapshot, strings.TrimSpace(modelName), strings.TrimSpace(promptVersion)}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode catalog fingerprint: %w", err)
	}
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("sha256:%x", sum[:]), nil
}

// canonicalCatalogSnapshot makes the source identity independent of collection
// order and nil-vs-empty slices. Collection and planning use different
// adapters, so the fingerprint boundary must define one canonical JSON shape
// rather than relying on each caller to prepare it identically.
func canonicalCatalogSnapshot(snapshot CatalogSnapshot) CatalogSnapshot {
	canonical := snapshot
	canonical.Videos = append([]CatalogVideo{}, snapshot.Videos...)
	canonical.SkippedVideos = append([]SkippedVideo{}, snapshot.SkippedVideos...)
	sort.SliceStable(canonical.Videos, func(i, j int) bool {
		return canonical.Videos[i].VideoID < canonical.Videos[j].VideoID
	})
	sort.SliceStable(canonical.SkippedVideos, func(i, j int) bool {
		return canonical.SkippedVideos[i].VideoID < canonical.SkippedVideos[j].VideoID
	})
	return canonical
}

func (video CatalogVideo) routingProfile() (*summary.OrchestrationProfile, error) {
	if video.OrchestrationProfile != nil && video.CompatibilityProfile != nil {
		return nil, fmt.Errorf("video %s has both orchestration and compatibility profiles", strings.TrimSpace(video.VideoID))
	}
	if video.OrchestrationProfile != nil {
		return video.OrchestrationProfile, nil
	}
	return video.CompatibilityProfile, nil
}

func (m ClusterMaterial) ValidateAgainst(cluster PlanCluster) error {
	return m.validateAgainst(cluster, true)
}

// ValidateRetrievedAgainst accepts a non-empty evidence subset selected by
// retrieval while preserving the plan's immutable video and evidence scope.
func (m ClusterMaterial) ValidateRetrievedAgainst(cluster PlanCluster) error {
	return m.validateAgainst(cluster, false)
}

func (m ClusterMaterial) validateAgainst(cluster PlanCluster, requireCompleteEvidence bool) error {
	if strings.TrimSpace(m.ContractVersion) != MaterialContractVersion {
		return fmt.Errorf("unsupported material contract version %q", m.ContractVersion)
	}
	if strings.TrimSpace(m.ClusterKey) == "" || m.ClusterKey != cluster.ClusterKey {
		return fmt.Errorf("cluster material key does not match plan cluster")
	}
	if cluster.ReviewStatus != PlanAccepted {
		return fmt.Errorf("cluster material requires an accepted plan cluster")
	}
	requestByVideo := make(map[string]MaterialRequest, len(cluster.MaterialRequests))
	for _, request := range cluster.MaterialRequests {
		videoID := strings.TrimSpace(request.VideoID)
		if videoID == "" {
			return fmt.Errorf("plan material request has empty video ID")
		}
		if _, duplicate := requestByVideo[videoID]; duplicate {
			return fmt.Errorf("plan repeats material request for video %s", videoID)
		}
		requestByVideo[videoID] = request
	}
	memberSet := make(map[string]struct{}, len(cluster.SourceVideoIDs))
	for _, videoID := range cluster.SourceVideoIDs {
		videoID = strings.TrimSpace(videoID)
		if videoID == "" {
			return fmt.Errorf("plan cluster has empty source video ID")
		}
		memberSet[videoID] = struct{}{}
	}
	if len(requestByVideo) != len(memberSet) || len(m.SourceVideoIDs) != len(memberSet) {
		return fmt.Errorf("cluster material source video count does not match plan")
	}
	seenMaterialVideos := make(map[string]struct{}, len(m.SourceVideoIDs))
	for _, videoID := range m.SourceVideoIDs {
		videoID = strings.TrimSpace(videoID)
		if _, ok := memberSet[videoID]; !ok {
			return fmt.Errorf("cluster material references video outside plan: %s", videoID)
		}
		if _, duplicate := seenMaterialVideos[videoID]; duplicate {
			return fmt.Errorf("cluster material repeats source video %s", videoID)
		}
		seenMaterialVideos[videoID] = struct{}{}
		if _, ok := requestByVideo[videoID]; !ok {
			return fmt.Errorf("cluster material has no plan request for video %s", videoID)
		}
	}
	if len(m.Evidence) == 0 {
		return fmt.Errorf("cluster material must contain evidence")
	}
	requiresSummaryBlocks := false
	for _, request := range requestByVideo {
		if len(request.SummaryBlockIDs) > 0 {
			requiresSummaryBlocks = true
			break
		}
	}
	if requiresSummaryBlocks && len(m.SummaryBlocks) == 0 {
		return fmt.Errorf("cluster material must contain requested summary blocks")
	}
	seenBlocks := make(map[string]struct{}, len(m.SummaryBlocks))
	for index, block := range m.SummaryBlocks {
		videoID, blockID := strings.TrimSpace(block.VideoID), strings.TrimSpace(block.BlockID)
		if videoID == "" || blockID == "" || strings.TrimSpace(block.Text) == "" {
			return fmt.Errorf("summary block %d is incomplete", index+1)
		}
		key := videoID + "\x00" + blockID
		if _, duplicate := seenBlocks[key]; duplicate {
			return fmt.Errorf("cluster material repeats summary block %s/%s", videoID, blockID)
		}
		seenBlocks[key] = struct{}{}
		request, ok := requestByVideo[videoID]
		if !ok || !contains(request.SummaryBlockIDs, blockID) {
			return fmt.Errorf("cluster material summary block %s/%s is outside plan", videoID, blockID)
		}
	}
	for videoID, request := range requestByVideo {
		for _, blockID := range request.SummaryBlockIDs {
			if _, ok := seenBlocks[videoID+"\x00"+strings.TrimSpace(blockID)]; !ok {
				return fmt.Errorf("cluster material is missing summary block %s/%s", videoID, blockID)
			}
		}
	}
	seenEvidence := make(map[string]struct{}, len(m.Evidence))
	evidenceVideos := make(map[string]struct{}, len(requestByVideo))
	for index, evidence := range m.Evidence {
		videoID, evidenceID := strings.TrimSpace(evidence.VideoID), strings.TrimSpace(evidence.EvidenceID)
		if videoID == "" || evidenceID == "" || strings.TrimSpace(evidence.Text) == "" || evidence.StartMs < 0 || evidence.EndMs <= evidence.StartMs || strings.TrimSpace(evidence.TranscriptGeneration) == "" {
			return fmt.Errorf("evidence %d is incomplete", index+1)
		}
		key := videoID + "\x00" + evidenceID
		if _, duplicate := seenEvidence[key]; duplicate {
			return fmt.Errorf("cluster material repeats evidence %s/%s", videoID, evidenceID)
		}
		seenEvidence[key] = struct{}{}
		request, ok := requestByVideo[videoID]
		if !ok || !contains(request.EvidenceIDs, evidenceID) || request.TranscriptGeneration != evidence.TranscriptGeneration {
			return fmt.Errorf("cluster material evidence %s/%s is outside plan", videoID, evidenceID)
		}
		evidenceVideos[videoID] = struct{}{}
	}
	for videoID, request := range requestByVideo {
		if _, ok := evidenceVideos[videoID]; !ok {
			return fmt.Errorf("cluster material has no retrieved evidence for video %s", videoID)
		}
		if !requireCompleteEvidence {
			continue
		}
		for _, evidenceID := range request.EvidenceIDs {
			if _, ok := seenEvidence[videoID+"\x00"+strings.TrimSpace(evidenceID)]; !ok {
				return fmt.Errorf("cluster material is missing evidence %s/%s", videoID, evidenceID)
			}
		}
	}
	return nil
}

func orchestrationProfileReferences(profile *summary.OrchestrationProfile) (map[string]struct{}, map[string]struct{}) {
	blocks := make(map[string]struct{})
	evidence := make(map[string]struct{})
	if profile == nil {
		return blocks, evidence
	}
	for _, unit := range profile.TopicUnits {
		for _, blockID := range unit.SummaryBlockIDs {
			blocks[strings.TrimSpace(blockID)] = struct{}{}
		}
		for _, ref := range unit.EvidenceRefs {
			evidence[strings.TrimSpace(ref.EvidenceSentenceID)] = struct{}{}
		}
	}
	return blocks, evidence
}

func validateRequestedIDs(ids []string, allowed map[string]struct{}, kind string, clusterIndex, requestIndex int) error {
	seen := make(map[string]struct{}, len(ids))
	for _, rawID := range ids {
		id := strings.TrimSpace(rawID)
		if id == "" {
			return fmt.Errorf("topic cluster %d material request %d has empty %s ID", clusterIndex, requestIndex, kind)
		}
		if _, duplicate := seen[id]; duplicate {
			return fmt.Errorf("topic cluster %d material request %d repeats %s %q", clusterIndex, requestIndex, kind, id)
		}
		seen[id] = struct{}{}
		if _, ok := allowed[id]; !ok {
			return fmt.Errorf("topic cluster %d material request %d references %s outside orchestration profile: %s", clusterIndex, requestIndex, kind, id)
		}
	}
	return nil
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == target {
			return true
		}
	}
	return false
}
