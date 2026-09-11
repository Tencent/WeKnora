package trainingorchestration

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/custom/service/knowledge"
)

const (
	// The first-release page can render at most 20 clusters. Each accepted
	// cluster is independently capped at 16 units by cluster generation, so a
	// 120-unit aggregate rejected otherwise-valid plans whenever the model
	// produced more than roughly six units per cluster. Keep an aggregate
	// guard, but align it with the per-cluster contract and leave headroom for
	// the 20-cluster upper bound without allowing an unbounded document.
	maxProjectionTopicClusters = 20
	maxProjectionLearningUnits = 240
)

type ProjectionDocument struct {
	TrainingPathProjection Projection `json:"training_path_projection"`
}

type Projection struct {
	SchemaVersion              string                 `json:"schema_version"`
	OwnerScopeID               string                 `json:"owner_scope_id"`
	SourceFingerprint          string                 `json:"source_fingerprint"`
	GeneratedAt                string                 `json:"generated_at"`
	RetrievalDegraded          bool                   `json:"retrieval_degraded,omitempty"`
	RetrievalDegradationReason string                 `json:"retrieval_degradation_reason,omitempty"`
	Statistics                 ProjectionStatistics   `json:"statistics"`
	TopicClusters              []TopicCluster         `json:"topic_clusters"`
	TopicClusterRelations      []TopicClusterRelation `json:"topic_cluster_relations"`
}

type ProjectionStatistics struct {
	ScannedVideos          int                    `json:"scanned_videos"`
	QualifiedVideos        int                    `json:"qualified_videos"`
	SelectedVideos         int                    `json:"selected_videos"`
	NotSelectedVideos      int                    `json:"not_selected_videos"`
	NotSelectedReasonCount NotSelectedReasonCount `json:"not_selected_reason_counts"`
	SkippedVideos          int                    `json:"skipped_videos"`
	SkippedReasonCounts    map[SkipReason]int     `json:"skipped_reason_counts"`
	TopicSourceCounts      map[TopicSource]int    `json:"topic_source_counts"`
	TopicClusterCount      int                    `json:"topic_cluster_count"`
	LearningUnitCount      int                    `json:"learning_unit_count"`
	SelectedKnowledgeCount int                    `json:"selected_knowledge_count"`
	LearningDurationSecs   int                    `json:"learning_duration_seconds"`
}

type NotSelectedReasonCount struct {
	RedundantEvidence int `json:"redundant_evidence"`
	OutputLimit       int `json:"output_limit"`
}

type EvidenceRef struct {
	VideoID              string `json:"video_id"`
	TranscriptGeneration string `json:"transcript_generation"`
	EvidenceID           string `json:"evidence_id"`
	StartMs              int    `json:"start_ms"`
	EndMs                int    `json:"end_ms"`
}

type KnowledgeRef struct {
	KnowledgeObjectID string                  `json:"knowledge_object_id"`
	WikiPageID        string                  `json:"wiki_page_id"`
	KnowledgeType     knowledge.KnowledgeType `json:"knowledge_type"`
}

type MemberTopic struct {
	TopicID string `json:"topic_id"`
	Title   string `json:"title"`
}

type LearningUnit struct {
	UnitID          string         `json:"unit_id"`
	LearningTitle   string         `json:"learning_title"`
	LearnerQuestion string         `json:"learner_question"`
	LearningOutcome string         `json:"learning_outcome"`
	Sequence        int            `json:"sequence"`
	KnowledgeRefs   []KnowledgeRef `json:"knowledge_refs"`
	EvidenceRefs    []EvidenceRef  `json:"evidence_refs"`
	Confidence      float64        `json:"confidence"`
	ReviewStatus    string         `json:"review_status"`
}

type LearningStage struct {
	StageID  string         `json:"stage_id"`
	Title    string         `json:"title"`
	Summary  string         `json:"summary"`
	Sequence int            `json:"sequence"`
	Units    []LearningUnit `json:"units"`
}

type LearningPath struct {
	PathID          string          `json:"path_id"`
	PrimaryTemplate string          `json:"primary_template"`
	Stages          []LearningStage `json:"stages"`
}

type TopicCluster struct {
	ClusterID           string        `json:"cluster_id"`
	Title               string        `json:"title"`
	Summary             string        `json:"summary"`
	LearningGoal        string        `json:"learning_goal"`
	LearningContentType string        `json:"learning_content_type"`
	MemberTopics        []MemberTopic `json:"member_topics"`
	SourceVideoIDs      []string      `json:"source_video_ids"`
	KnowledgeObjectIDs  []string      `json:"knowledge_object_ids"`
	EvidenceRefs        []EvidenceRef `json:"evidence_refs"`
	Confidence          float64       `json:"confidence"`
	ReviewStatus        string        `json:"review_status"`
	Path                LearningPath  `json:"path"`
}

type TopicClusterRelation struct {
	RelationID          string        `json:"relation_id"`
	SourceClusterID     string        `json:"source_cluster_id"`
	TargetClusterID     string        `json:"target_cluster_id"`
	RelationType        string        `json:"relation_type"`
	Summary             string        `json:"summary"`
	SourceKnowledgeRefs []string      `json:"source_knowledge_refs"`
	TargetKnowledgeRefs []string      `json:"target_knowledge_refs"`
	SourceEvidenceRefs  []EvidenceRef `json:"source_evidence_refs"`
	TargetEvidenceRefs  []EvidenceRef `json:"target_evidence_refs"`
	Confidence          float64       `json:"confidence"`
	ReviewStatus        string        `json:"review_status"`
}

var allowedTemplates = map[string]struct{}{
	"skill_method": {}, "tool_operation": {}, "concept_cognition": {},
	"case_analysis": {}, "humanities_reflection": {}, "process_standard": {},
}

var allowedRelationTypes = map[string]struct{}{
	"required_before": {}, "recommended_before": {}, "application": {},
	"complementary": {}, "contrast": {},
}

func withoutKnowledgeObjects(input InputPackage) InputPackage {
	clean := input
	clean.QualifiedVideos = make([]VideoTopicProfile, len(input.QualifiedVideos))
	for i, video := range input.QualifiedVideos {
		cleanVideo := video
		cleanVideo.KnowledgeIndex = WikiReference{}
		cleanVideo.KnowledgeSignals = []KnowledgeSignal{}
		if video.Summary != nil {
			cleanSummary := *video.Summary
			cleanSummary.Signals = make([]SummarySignal, len(video.Summary.Signals))
			for j, signal := range video.Summary.Signals {
				cleanSignal := signal
				cleanSignal.KnowledgeRefs = []string{}
				cleanSummary.Signals[j] = cleanSignal
			}
			cleanVideo.Summary = &cleanSummary
		}
		clean.QualifiedVideos[i] = cleanVideo
	}
	return clean
}

func normalizeProjectionKnowledgeFields(projection *Projection) {
	if projection == nil {
		return
	}
	projection.Statistics.SelectedKnowledgeCount = 0
	for i := range projection.TopicClusters {
		cluster := &projection.TopicClusters[i]
		cluster.KnowledgeObjectIDs = []string{}
		for j := range cluster.Path.Stages {
			for k := range cluster.Path.Stages[j].Units {
				cluster.Path.Stages[j].Units[k].KnowledgeRefs = []KnowledgeRef{}
			}
		}
	}
	for i := range projection.TopicClusterRelations {
		projection.TopicClusterRelations[i].SourceKnowledgeRefs = []string{}
		projection.TopicClusterRelations[i].TargetKnowledgeRefs = []string{}
	}
}

func ValidateProjection(doc ProjectionDocument, input InputPackage) error {
	p := doc.TrainingPathProjection
	if p.SchemaVersion != SchemaVersion || strings.TrimSpace(p.OwnerScopeID) == "" || p.OwnerScopeID != input.OwnerScopeID || strings.TrimSpace(p.SourceFingerprint) == "" {
		return fmt.Errorf("projection identity does not match the collected input")
	}
	if _, err := time.Parse(time.RFC3339Nano, p.GeneratedAt); err != nil {
		return fmt.Errorf("projection generated_at is invalid: %w", err)
	}
	if err := validateKnowledgeCompatibilityFields(doc); err != nil {
		return err
	}
	if err := validateProjectionIDs(p); err != nil {
		return err
	}
	videos, evidence := buildInputWhitelist(input)
	clusterIDs := make(map[string]struct{}, len(p.TopicClusters))
	unitIDs := make(map[string]struct{})
	selectedVideos := make(map[string]struct{})
	allUnitEvidence := make([]EvidenceRef, 0)
	for i, cluster := range p.TopicClusters {
		if err := validateCluster(cluster, videos, evidence, clusterIDs, unitIDs, selectedVideos, &allUnitEvidence); err != nil {
			return fmt.Errorf("topic_clusters[%d]: %w", i, err)
		}
	}
	if len(p.TopicClusters) > maxProjectionTopicClusters || len(unitIDs) > maxProjectionLearningUnits {
		return fmt.Errorf("projection exceeds the first-release output capacity")
	}
	relationIDs := make(map[string]struct{}, len(p.TopicClusterRelations))
	for i, relation := range p.TopicClusterRelations {
		if err := validateRelation(relation, p.TopicClusters, clusterIDs, evidence, relationIDs); err != nil {
			return fmt.Errorf("topic_cluster_relations[%d]: %w", i, err)
		}
	}
	if hasRequiredBeforeCycle(p.TopicClusterRelations) {
		return fmt.Errorf("required_before relations contain a cycle")
	}
	expected := buildStatistics(input, p.TopicClusters, selectedVideos, allUnitEvidence)
	if fmt.Sprintf("%#v", p.Statistics) != fmt.Sprintf("%#v", expected) {
		return fmt.Errorf("projection statistics do not match the published content")
	}
	return nil
}

func validateKnowledgeCompatibilityFields(doc ProjectionDocument) error {
	p := doc.TrainingPathProjection
	if p.TopicClusters == nil || p.TopicClusterRelations == nil {
		return fmt.Errorf("topic arrays must be JSON arrays")
	}
	if p.Statistics.SelectedKnowledgeCount != 0 {
		return fmt.Errorf("selected knowledge count must be zero for this stage")
	}
	for _, cluster := range p.TopicClusters {
		if cluster.KnowledgeObjectIDs == nil || len(cluster.KnowledgeObjectIDs) != 0 {
			return fmt.Errorf("knowledge object references are disabled for this stage")
		}
		for _, stage := range cluster.Path.Stages {
			for _, unit := range stage.Units {
				if unit.KnowledgeRefs == nil || len(unit.KnowledgeRefs) != 0 {
					return fmt.Errorf("learning unit knowledge references are disabled for this stage")
				}
			}
		}
	}
	for _, relation := range p.TopicClusterRelations {
		if relation.SourceKnowledgeRefs == nil || relation.TargetKnowledgeRefs == nil || len(relation.SourceKnowledgeRefs) != 0 || len(relation.TargetKnowledgeRefs) != 0 {
			return fmt.Errorf("relation knowledge references are disabled for this stage")
		}
	}
	return nil
}

func validateProjectionIDs(p Projection) error {
	seen := map[string]string{}
	add := func(id, kind string) error {
		if strings.TrimSpace(id) == "" {
			return nil
		}
		if previous, duplicate := seen[id]; duplicate {
			return fmt.Errorf("duplicate projection id %s used by %s and %s", id, previous, kind)
		}
		seen[id] = kind
		return nil
	}
	for _, cluster := range p.TopicClusters {
		if err := add(cluster.ClusterID, "cluster"); err != nil {
			return err
		}
		if err := add(cluster.Path.PathID, "path"); err != nil {
			return err
		}
		for _, topic := range cluster.MemberTopics {
			if err := add(topic.TopicID, "topic"); err != nil {
				return err
			}
		}
		for _, stage := range cluster.Path.Stages {
			if err := add(stage.StageID, "stage"); err != nil {
				return err
			}
			for _, unit := range stage.Units {
				if err := add(unit.UnitID, "unit"); err != nil {
					return err
				}
			}
		}
	}
	for _, relation := range p.TopicClusterRelations {
		if err := add(relation.RelationID, "relation"); err != nil {
			return err
		}
	}
	return nil
}

func validateCluster(cluster TopicCluster, videos map[string]VideoTopicProfile, evidence map[string]EvidenceSignal, clusterIDs, unitIDs, selectedVideos map[string]struct{}, allUnitEvidence *[]EvidenceRef) error {
	if !nonEmpty(cluster.ClusterID, cluster.Title, cluster.Summary, cluster.LearningGoal) || cluster.ReviewStatus != "passed" || !validConfidence(cluster.Confidence) {
		return fmt.Errorf("required cluster fields are invalid")
	}
	if _, duplicate := clusterIDs[cluster.ClusterID]; duplicate {
		return fmt.Errorf("duplicate cluster_id %s", cluster.ClusterID)
	}
	clusterIDs[cluster.ClusterID] = struct{}{}
	if _, ok := allowedTemplates[cluster.LearningContentType]; !ok || cluster.Path.PrimaryTemplate != cluster.LearningContentType {
		return fmt.Errorf("learning content type and path template must match")
	}
	if len(cluster.MemberTopics) == 0 || len(cluster.SourceVideoIDs) == 0 || len(cluster.EvidenceRefs) == 0 || len(cluster.Path.Stages) == 0 || strings.TrimSpace(cluster.Path.PathID) == "" {
		return fmt.Errorf("cluster collections must not be empty")
	}
	memberIDs := map[string]struct{}{}
	for _, topic := range cluster.MemberTopics {
		if !nonEmpty(topic.TopicID, topic.Title) {
			return fmt.Errorf("member topic is invalid")
		}
		if _, duplicate := memberIDs[topic.TopicID]; duplicate {
			return fmt.Errorf("duplicate topic_id %s", topic.TopicID)
		}
		memberIDs[topic.TopicID] = struct{}{}
	}
	clusterVideos := map[string]struct{}{}
	for _, id := range cluster.SourceVideoIDs {
		if _, ok := videos[id]; !ok {
			return fmt.Errorf("video %s is outside the input whitelist", id)
		}
		if _, duplicate := clusterVideos[id]; duplicate {
			return fmt.Errorf("duplicate source video %s", id)
		}
		clusterVideos[id] = struct{}{}
		selectedVideos[id] = struct{}{}
	}
	for _, ref := range cluster.EvidenceRefs {
		if err := validateEvidenceRef(ref, evidence); err != nil {
			return err
		}
		if _, ok := clusterVideos[ref.VideoID]; !ok {
			return fmt.Errorf("cluster evidence video is not a source video")
		}
	}
	stageSequences := map[int]struct{}{}
	for _, stage := range cluster.Path.Stages {
		if !nonEmpty(stage.StageID, stage.Title, stage.Summary) || stage.Sequence < 1 || len(stage.Units) == 0 {
			return fmt.Errorf("learning stage is invalid")
		}
		if _, duplicate := stageSequences[stage.Sequence]; duplicate {
			return fmt.Errorf("duplicate stage sequence %d", stage.Sequence)
		}
		stageSequences[stage.Sequence] = struct{}{}
		unitSequences := map[int]struct{}{}
		for _, unit := range stage.Units {
			if !nonEmpty(unit.UnitID, unit.LearningTitle, unit.LearnerQuestion, unit.LearningOutcome) || unit.Sequence < 1 || unit.ReviewStatus != "passed" || !validConfidence(unit.Confidence) || len(unit.EvidenceRefs) == 0 {
				return fmt.Errorf("learning unit is invalid")
			}
			if _, duplicate := unitIDs[unit.UnitID]; duplicate {
				return fmt.Errorf("duplicate unit_id %s", unit.UnitID)
			}
			unitIDs[unit.UnitID] = struct{}{}
			if _, duplicate := unitSequences[unit.Sequence]; duplicate {
				return fmt.Errorf("duplicate unit sequence %d", unit.Sequence)
			}
			unitSequences[unit.Sequence] = struct{}{}
			for _, ref := range unit.EvidenceRefs {
				if err := validateEvidenceRef(ref, evidence); err != nil {
					return err
				}
				if _, ok := clusterVideos[ref.VideoID]; !ok {
					return fmt.Errorf("learning unit evidence is outside its cluster")
				}
				*allUnitEvidence = append(*allUnitEvidence, ref)
			}
		}
	}
	return nil
}

func validateRelation(relation TopicClusterRelation, clusters []TopicCluster, clusterIDs map[string]struct{}, evidence map[string]EvidenceSignal, relationIDs map[string]struct{}) error {
	if !nonEmpty(relation.RelationID, relation.SourceClusterID, relation.TargetClusterID, relation.Summary) || relation.SourceClusterID == relation.TargetClusterID || relation.ReviewStatus != "passed" || !validConfidence(relation.Confidence) {
		return fmt.Errorf("required relation fields are invalid")
	}
	if _, duplicate := relationIDs[relation.RelationID]; duplicate {
		return fmt.Errorf("duplicate relation_id %s", relation.RelationID)
	}
	relationIDs[relation.RelationID] = struct{}{}
	if _, ok := clusterIDs[relation.SourceClusterID]; !ok {
		return fmt.Errorf("source cluster does not exist")
	}
	if _, ok := clusterIDs[relation.TargetClusterID]; !ok {
		return fmt.Errorf("target cluster does not exist")
	}
	if _, ok := allowedRelationTypes[relation.RelationType]; !ok {
		return fmt.Errorf("unsupported relation type %s", relation.RelationType)
	}
	if (relation.RelationType == "complementary" || relation.RelationType == "contrast") && relation.SourceClusterID > relation.TargetClusterID {
		return fmt.Errorf("undirected relation endpoints are not canonical")
	}
	if len(relation.SourceEvidenceRefs) == 0 || len(relation.TargetEvidenceRefs) == 0 {
		return fmt.Errorf("both relation endpoints require evidence")
	}
	clusterByID := map[string]TopicCluster{}
	for _, cluster := range clusters {
		clusterByID[cluster.ClusterID] = cluster
	}
	for _, side := range []struct {
		refs    []EvidenceRef
		cluster TopicCluster
	}{{relation.SourceEvidenceRefs, clusterByID[relation.SourceClusterID]}, {relation.TargetEvidenceRefs, clusterByID[relation.TargetClusterID]}} {
		clusterEvidence := make(map[string]struct{}, len(side.cluster.EvidenceRefs))
		for _, ref := range side.cluster.EvidenceRefs {
			clusterEvidence[evidenceRefKey(ref)] = struct{}{}
		}
		for _, ref := range side.refs {
			if err := validateEvidenceRef(ref, evidence); err != nil {
				return err
			}
			if _, ok := clusterEvidence[evidenceRefKey(ref)]; !ok {
				return fmt.Errorf("relation evidence is outside its endpoint cluster")
			}
		}
	}
	return nil
}

func buildInputWhitelist(input InputPackage) (map[string]VideoTopicProfile, map[string]EvidenceSignal) {
	videos := map[string]VideoTopicProfile{}
	evidence := map[string]EvidenceSignal{}
	for _, video := range input.QualifiedVideos {
		videos[video.VideoID] = video
		for _, signal := range video.EvidenceSignals {
			evidence[evidenceKey(signal.VideoID, signal.TranscriptGeneration, signal.EvidenceID)] = signal
		}
	}
	return videos, evidence
}

func validateEvidenceRef(ref EvidenceRef, whitelist map[string]EvidenceSignal) error {
	allowed, ok := whitelist[evidenceKey(ref.VideoID, ref.TranscriptGeneration, ref.EvidenceID)]
	if !ok || allowed.StartMs != ref.StartMs || allowed.EndMs != ref.EndMs {
		return fmt.Errorf("evidence reference is outside the current input whitelist")
	}
	return nil
}

func evidenceKey(videoID, generation, evidenceID string) string {
	return videoID + "\x00" + generation + "\x00" + evidenceID
}
func evidenceRefKey(ref EvidenceRef) string {
	return fmt.Sprintf("%s\x00%s\x00%s\x00%d\x00%d", ref.VideoID, ref.TranscriptGeneration, ref.EvidenceID, ref.StartMs, ref.EndMs)
}
func nonEmpty(values ...string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return false
		}
	}
	return true
}
func validConfidence(value float64) bool { return value >= 0 && value <= 1 }

func hasRequiredBeforeCycle(relations []TopicClusterRelation) bool {
	edges := map[string][]string{}
	for _, r := range relations {
		if r.RelationType == "required_before" {
			edges[r.SourceClusterID] = append(edges[r.SourceClusterID], r.TargetClusterID)
		}
	}
	visiting, visited := map[string]bool{}, map[string]bool{}
	var visit func(string) bool
	visit = func(id string) bool {
		if visiting[id] {
			return true
		}
		if visited[id] {
			return false
		}
		visiting[id] = true
		for _, next := range edges[id] {
			if visit(next) {
				return true
			}
		}
		visiting[id] = false
		visited[id] = true
		return false
	}
	for id := range edges {
		if visit(id) {
			return true
		}
	}
	return false
}

func buildStatistics(input InputPackage, clusters []TopicCluster, selectedVideos map[string]struct{}, unitEvidence []EvidenceRef) ProjectionStatistics {
	notSelected := len(input.QualifiedVideos) - len(selectedVideos)
	learningUnitCount := 0
	for _, cluster := range clusters {
		for _, stage := range cluster.Path.Stages {
			learningUnitCount += len(stage.Units)
		}
	}
	stats := ProjectionStatistics{ScannedVideos: input.ScannedVideos, QualifiedVideos: len(input.QualifiedVideos), SelectedVideos: len(selectedVideos), NotSelectedVideos: notSelected, SkippedVideos: len(input.SkippedVideos), TopicClusterCount: len(clusters), LearningUnitCount: learningUnitCount, SelectedKnowledgeCount: 0, LearningDurationSecs: mergedEvidenceDurationSeconds(unitEvidence), SkippedReasonCounts: input.SkipReasonCounts, TopicSourceCounts: input.TopicSourceCounts}
	stats.NotSelectedReasonCount.RedundantEvidence = notSelected
	return stats
}

func mergedEvidenceDurationSeconds(refs []EvidenceRef) int {
	type interval struct{ start, end int }
	grouped := map[string][]interval{}
	for _, ref := range refs {
		key := ref.VideoID + "\x00" + ref.TranscriptGeneration
		grouped[key] = append(grouped[key], interval{ref.StartMs, ref.EndMs})
	}
	total := 0
	for _, intervals := range grouped {
		sort.Slice(intervals, func(i, j int) bool {
			if intervals[i].start == intervals[j].start {
				return intervals[i].end < intervals[j].end
			}
			return intervals[i].start < intervals[j].start
		})
		start, end := -1, -1
		for _, current := range intervals {
			if start < 0 {
				start, end = current.start, current.end
			} else if current.start <= end {
				if current.end > end {
					end = current.end
				}
			} else {
				total += end - start
				start, end = current.start, current.end
			}
		}
		if start >= 0 {
			total += end - start
		}
	}
	return (total + 999) / 1000
}
