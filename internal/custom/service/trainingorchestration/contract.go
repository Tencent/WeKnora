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
	Title             string                  `json:"title"`
	LocatorEvidence   EvidenceRef             `json:"locator_evidence"`
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
	ClusterID           string             `json:"cluster_id"`
	Title               string             `json:"title"`
	Summary             string             `json:"summary"`
	LearningGoal        string             `json:"learning_goal"`
	LearningContentType string             `json:"learning_content_type"`
	MemberTopics        []MemberTopic      `json:"member_topics"`
	SourceVideoIDs      []string           `json:"source_video_ids"`
	KnowledgeObjectIDs  []string           `json:"knowledge_object_ids"`
	EvidenceRefs        []EvidenceRef      `json:"evidence_refs"`
	Confidence          float64            `json:"confidence"`
	ReviewStatus        string             `json:"review_status"`
	GapAnalysis         ContentGapAnalysis `json:"gap_analysis"`
	Path                LearningPath       `json:"path"`
	EvidenceText        map[string]string  `json:"-"`
}

// ContentGapAnalysis is an independent assessment of whether the published
// path is deep enough for the topic's stated capability. It never replaces or
// mutates the topic, stages, or learning units.
type ContentGapAnalysis struct {
	Status                     string        `json:"status"`
	CurrentDepthSummary        string        `json:"current_depth_summary"`
	SupplementDirectionSummary string        `json:"supplement_direction_summary"`
	Dimensions                 GapDimensions `json:"dimensions"`
}

type GapDimensions struct {
	KnowledgeCoverage         GapDimension `json:"knowledge_coverage"`
	WorkplaceApplication      GapDimension `json:"workplace_application"`
	IndependentTaskCompletion GapDimension `json:"independent_task_completion"`
}

type GapDimension struct {
	Gap            string `json:"gap"`
	Recommendation string `json:"recommendation"`
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
	for i := range projection.TopicClusters {
		cluster := &projection.TopicClusters[i]
		cluster.KnowledgeObjectIDs = uniqueSortedStrings(cluster.KnowledgeObjectIDs)
		for j := range cluster.Path.Stages {
			for k := range cluster.Path.Stages[j].Units {
				cluster.Path.Stages[j].Units[k].KnowledgeRefs = normalizeKnowledgeRefs(cluster.Path.Stages[j].Units[k].KnowledgeRefs)
			}
		}
	}
	for i := range projection.TopicClusterRelations {
		projection.TopicClusterRelations[i].SourceKnowledgeRefs = uniqueSortedStrings(projection.TopicClusterRelations[i].SourceKnowledgeRefs)
		projection.TopicClusterRelations[i].TargetKnowledgeRefs = uniqueSortedStrings(projection.TopicClusterRelations[i].TargetKnowledgeRefs)
	}
}

func ensureGapAnalyses(projection *Projection) {
	if projection == nil {
		return
	}
	for index := range projection.TopicClusters {
		cluster := &projection.TopicClusters[index]
		if strings.TrimSpace(cluster.GapAnalysis.Status) == "" {
			cluster.GapAnalysis = buildContentGapAnalysis(*cluster)
		}
	}
}

// buildContentGapAnalysis intentionally uses only the already-published path
// shape. It provides a stable, business-language fallback when an older
// generator did not emit the independent assessment yet.
func buildContentGapAnalysis(cluster TopicCluster) ContentGapAnalysis {
	unitCount := 0
	evidenceCount := 0
	for _, stage := range cluster.Path.Stages {
		for _, unit := range stage.Units {
			unitCount++
			evidenceCount += len(unit.EvidenceRefs)
		}
	}
	goal := strings.TrimSpace(cluster.LearningGoal)
	if goal == "" {
		goal = strings.TrimSpace(cluster.Title)
	}
	return ContentGapAnalysis{
		Status:                     "partial",
		CurrentDepthSummary:        fmt.Sprintf("围绕“%s”，当前路径提供 %d 个学习任务和 %d 条证据，已形成从理解到初步运用的学习基础。", goal, unitCount, evidenceCount),
		SupplementDirectionSummary: fmt.Sprintf("要达到“%s”所要求的能力，还需要用更多边界情境和可验收任务验证迁移。", goal),
		Dimensions: GapDimensions{
			KnowledgeCoverage:         GapDimension{Gap: fmt.Sprintf("学习者对“%s”涉及的关键概念和适用边界仍缺少完整判断。", goal), Recommendation: "补充概念辨析、判断条件和反例证据。"},
			WorkplaceApplication:      GapDimension{Gap: fmt.Sprintf("学习者还不能稳定把“%s”迁移到变化的工作情境。", goal), Recommendation: "补充岗位案例、操作步骤与异常处理练习。"},
			IndependentTaskCompletion: GapDimension{Gap: fmt.Sprintf("学习者还不能独立交付与“%s”对应且可检查的任务结果。", goal), Recommendation: "补充从输入、执行到验收标准的完整任务。"},
		},
	}
}

// bindProjectionKnowledgeFields is the program-owned knowledge seam. Model
// output may contain no knowledge IDs (and any such IDs are ignored); audited
// objects are attached only when their evidence contribution supports the
// generated unit.
func bindProjectionKnowledgeFields(projection *Projection, input InputPackage) {
	if projection == nil {
		return
	}
	objects := make([]MaterialKnowledge, 0)
	for _, video := range input.QualifiedVideos {
		for _, signal := range video.KnowledgeSignals {
			objects = append(objects, MaterialKnowledge{
				VideoID: video.VideoID, KnowledgeObjectID: signal.KnowledgeObjectID,
				WikiPageID: signal.WikiPageID, KnowledgeType: signal.KnowledgeType,
				Title:       signal.Title,
				EvidenceIDs: append([]string(nil), signal.EvidenceIDs...),
			})
		}
	}
	for clusterIndex := range projection.TopicClusters {
		cluster := &projection.TopicClusters[clusterIndex]
		cluster.KnowledgeObjectIDs = []string{}
		for stageIndex := range cluster.Path.Stages {
			for unitIndex := range cluster.Path.Stages[stageIndex].Units {
				unit := &cluster.Path.Stages[stageIndex].Units[unitIndex]
				unit.KnowledgeRefs = knowledgeRefsForEvidence(objects, unit.EvidenceRefs)
				for _, ref := range unit.KnowledgeRefs {
					cluster.KnowledgeObjectIDs = appendUnique(cluster.KnowledgeObjectIDs, ref.KnowledgeObjectID)
				}
			}
		}
		cluster.KnowledgeObjectIDs = uniqueSortedStrings(cluster.KnowledgeObjectIDs)
	}
	for relationIndex := range projection.TopicClusterRelations {
		relation := &projection.TopicClusterRelations[relationIndex]
		relation.SourceKnowledgeRefs = knowledgeIDsForEvidence(objects, relation.SourceEvidenceRefs)
		relation.TargetKnowledgeRefs = knowledgeIDsForEvidence(objects, relation.TargetEvidenceRefs)
	}
	normalizeProjectionKnowledgeFields(projection)
}

func knowledgeIDsForEvidence(objects []MaterialKnowledge, evidence []EvidenceRef) []string {
	refs := knowledgeRefsForEvidence(objects, evidence)
	ids := make([]string, 0, len(refs))
	for _, ref := range refs {
		ids = append(ids, ref.KnowledgeObjectID)
	}
	return uniqueSortedStrings(ids)
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
	knowledgeWhitelist := buildKnowledgeWhitelist(input)
	clusterIDs := make(map[string]struct{}, len(p.TopicClusters))
	unitIDs := make(map[string]struct{})
	selectedVideos := make(map[string]struct{})
	selectedKnowledge := make(map[string]struct{})
	allUnitEvidence := make([]EvidenceRef, 0)
	for i, cluster := range p.TopicClusters {
		if err := validateGapAnalysis(cluster.GapAnalysis); err != nil {
			return fmt.Errorf("topic_clusters[%d]: %w", i, err)
		}
		if err := validateCluster(cluster, videos, evidence, knowledgeWhitelist, clusterIDs, unitIDs, selectedVideos, selectedKnowledge, &allUnitEvidence); err != nil {
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
	expected.SelectedKnowledgeCount = len(selectedKnowledge)
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
	for _, cluster := range p.TopicClusters {
		if cluster.KnowledgeObjectIDs == nil {
			return fmt.Errorf("topic cluster knowledge_object_ids must be a JSON array")
		}
		for _, stage := range cluster.Path.Stages {
			for _, unit := range stage.Units {
				if unit.KnowledgeRefs == nil {
					return fmt.Errorf("learning unit knowledge_refs must be a JSON array")
				}
			}
		}
	}
	for _, relation := range p.TopicClusterRelations {
		if relation.SourceKnowledgeRefs == nil || relation.TargetKnowledgeRefs == nil {
			return fmt.Errorf("relation knowledge references must be JSON arrays")
		}
	}
	return nil
}

func validateGapAnalysis(analysis ContentGapAnalysis) error {
	status := strings.TrimSpace(analysis.Status)
	if status != "partial" && status != "sufficient" {
		return fmt.Errorf("gap_analysis status must be partial or sufficient")
	}
	if strings.TrimSpace(analysis.CurrentDepthSummary) == "" || strings.TrimSpace(analysis.SupplementDirectionSummary) == "" {
		return fmt.Errorf("gap_analysis summaries are required")
	}
	for name, dimension := range map[string]GapDimension{
		"knowledge_coverage":          analysis.Dimensions.KnowledgeCoverage,
		"workplace_application":       analysis.Dimensions.WorkplaceApplication,
		"independent_task_completion": analysis.Dimensions.IndependentTaskCompletion,
	} {
		if strings.TrimSpace(dimension.Gap) == "" || strings.TrimSpace(dimension.Recommendation) == "" {
			return fmt.Errorf("gap_analysis dimension %s is incomplete", name)
		}
	}
	return nil
}

func normalizeKnowledgeRefs(refs []KnowledgeRef) []KnowledgeRef {
	result := make([]KnowledgeRef, 0, len(refs))
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		ref.KnowledgeObjectID = strings.TrimSpace(ref.KnowledgeObjectID)
		ref.WikiPageID = strings.TrimSpace(ref.WikiPageID)
		ref.KnowledgeType = knowledge.KnowledgeType(strings.ToLower(strings.TrimSpace(string(ref.KnowledgeType))))
		ref.Title = strings.TrimSpace(ref.Title)
		ref.LocatorEvidence.VideoID = strings.TrimSpace(ref.LocatorEvidence.VideoID)
		ref.LocatorEvidence.TranscriptGeneration = strings.TrimSpace(ref.LocatorEvidence.TranscriptGeneration)
		ref.LocatorEvidence.EvidenceID = strings.TrimSpace(ref.LocatorEvidence.EvidenceID)
		if ref.KnowledgeObjectID == "" || ref.WikiPageID == "" || ref.Title == "" || !knowledge.IsKnowledgeType(ref.KnowledgeType) ||
			ref.LocatorEvidence.VideoID == "" || ref.LocatorEvidence.TranscriptGeneration == "" || ref.LocatorEvidence.EvidenceID == "" ||
			ref.LocatorEvidence.StartMs < 0 || ref.LocatorEvidence.EndMs <= ref.LocatorEvidence.StartMs {
			continue
		}
		if _, ok := seen[ref.KnowledgeObjectID]; ok {
			continue
		}
		seen[ref.KnowledgeObjectID] = struct{}{}
		result = append(result, ref)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].KnowledgeObjectID != result[j].KnowledgeObjectID {
			return result[i].KnowledgeObjectID < result[j].KnowledgeObjectID
		}
		return result[i].WikiPageID < result[j].WikiPageID
	})
	return result
}

func uniqueSortedStrings(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
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

func validateCluster(cluster TopicCluster, videos map[string]VideoTopicProfile, evidence map[string]EvidenceSignal, knowledgeWhitelist map[string][]MaterialKnowledge, clusterIDs, unitIDs, selectedVideos, selectedKnowledge map[string]struct{}, allUnitEvidence *[]EvidenceRef) error {
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
	if cluster.KnowledgeObjectIDs == nil {
		return fmt.Errorf("knowledge_object_ids must be a JSON array")
	}
	for _, objectID := range cluster.KnowledgeObjectIDs {
		objectID = strings.TrimSpace(objectID)
		if objectID == "" {
			return fmt.Errorf("knowledge_object_ids contains an empty ID")
		}
		selectedKnowledge[objectID] = struct{}{}
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
			if unit.KnowledgeRefs == nil {
				return fmt.Errorf("learning unit knowledge_refs must be a JSON array")
			}
			for _, ref := range unit.KnowledgeRefs {
				key := strings.TrimSpace(ref.KnowledgeObjectID)
				allowedItems, ok := knowledgeWhitelist[key]
				if !ok {
					return fmt.Errorf("learning unit knowledge reference %s is outside the audited knowledge whitelist", key)
				}
				matched := false
				for _, allowed := range allowedItems {
					if allowed.WikiPageID != strings.TrimSpace(ref.WikiPageID) ||
						allowed.KnowledgeType != ref.KnowledgeType ||
						strings.TrimSpace(allowed.Title) != strings.TrimSpace(ref.Title) {
						continue
					}
					for _, evidenceRef := range unit.EvidenceRefs {
						if evidenceRefKey(evidenceRef) == evidenceRefKey(ref.LocatorEvidence) && contains(allowed.EvidenceIDs, evidenceRef.EvidenceID) && allowed.VideoID == evidenceRef.VideoID {
							matched = true
							break
						}
					}
					if matched {
						break
					}
				}
				if !matched {
					return fmt.Errorf("learning unit knowledge reference %s has no supporting unit evidence", key)
				}
				selectedKnowledge[key] = struct{}{}
			}
		}
	}
	clusterKnowledge := make(map[string]struct{}, len(cluster.KnowledgeObjectIDs))
	for _, objectID := range cluster.KnowledgeObjectIDs {
		clusterKnowledge[strings.TrimSpace(objectID)] = struct{}{}
	}
	for objectID := range clusterKnowledge {
		found := false
		for _, stage := range cluster.Path.Stages {
			for _, unit := range stage.Units {
				for _, ref := range unit.KnowledgeRefs {
					if strings.TrimSpace(ref.KnowledgeObjectID) == objectID {
						found = true
					}
				}
			}
		}
		if !found {
			return fmt.Errorf("cluster knowledge object %s is not referenced by a learning unit", objectID)
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

func buildKnowledgeWhitelist(input InputPackage) map[string][]MaterialKnowledge {
	result := make(map[string][]MaterialKnowledge)
	for _, video := range input.QualifiedVideos {
		for _, signal := range video.KnowledgeSignals {
			id := strings.TrimSpace(signal.KnowledgeObjectID)
			if id == "" {
				continue
			}
			result[id] = append(result[id], MaterialKnowledge{
				KnowledgeObjectID: id,
				WikiPageID:        strings.TrimSpace(signal.WikiPageID),
				KnowledgeType:     signal.KnowledgeType,
				Title:             strings.TrimSpace(signal.Title),
				VideoID:           strings.TrimSpace(signal.SourceVideoID),
				EvidenceIDs:       append([]string(nil), signal.EvidenceIDs...),
			})
		}
	}
	return result
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
	knowledgeObjects := make(map[string]struct{})
	for _, cluster := range clusters {
		for _, objectID := range cluster.KnowledgeObjectIDs {
			if id := strings.TrimSpace(objectID); id != "" {
				knowledgeObjects[id] = struct{}{}
			}
		}
		for _, stage := range cluster.Path.Stages {
			learningUnitCount += len(stage.Units)
			for _, unit := range stage.Units {
				for _, ref := range unit.KnowledgeRefs {
					if id := strings.TrimSpace(ref.KnowledgeObjectID); id != "" {
						knowledgeObjects[id] = struct{}{}
					}
				}
			}
		}
	}
	stats := ProjectionStatistics{ScannedVideos: input.ScannedVideos, QualifiedVideos: len(input.QualifiedVideos), SelectedVideos: len(selectedVideos), NotSelectedVideos: notSelected, SkippedVideos: len(input.SkippedVideos), TopicClusterCount: len(clusters), LearningUnitCount: learningUnitCount, SelectedKnowledgeCount: len(knowledgeObjects), LearningDurationSecs: mergedEvidenceDurationSeconds(unitEvidence), SkippedReasonCounts: input.SkipReasonCounts, TopicSourceCounts: input.TopicSourceCounts}
	stats.NotSelectedReasonCount.RedundantEvidence = notSelected
	return stats
}

func selectedVideoIDs(clusters []TopicCluster) map[string]struct{} {
	result := make(map[string]struct{})
	for _, cluster := range clusters {
		for _, videoID := range cluster.SourceVideoIDs {
			if id := strings.TrimSpace(videoID); id != "" {
				result[id] = struct{}{}
			}
		}
	}
	return result
}

func allProjectionEvidence(clusters []TopicCluster) []EvidenceRef {
	result := make([]EvidenceRef, 0)
	for _, cluster := range clusters {
		for _, stage := range cluster.Path.Stages {
			for _, unit := range stage.Units {
				result = append(result, unit.EvidenceRefs...)
			}
		}
	}
	return result
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
