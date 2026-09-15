package meetingorchestration

import (
	"fmt"
	"strings"
	"time"
)

const SchemaVersion = "meeting-orchestration/v1"

const (
	RelationPrerequisite       = "prerequisite"
	RelationConflictConstraint = "conflict_constraint"
	RelationSharedSupport      = "shared_support"
	RelationResultFeedback     = "result_feedback"
)

type Projection struct {
	SchemaVersion     string          `json:"schema_version"`
	OwnerScopeID      string          `json:"owner_scope_id"`
	SourceFingerprint string          `json:"source_fingerprint"`
	GeneratedAt       time.Time       `json:"generated_at"`
	Statistics        Statistics      `json:"statistics"`
	TopicClusters     []TopicCluster  `json:"topic_clusters"`
	TopicRelations    []TopicRelation `json:"topic_cluster_relations"`
}

type Statistics struct {
	ScannedVideos     int `json:"scanned_videos"`
	QualifiedVideos   int `json:"qualified_videos"`
	SkippedVideos     int `json:"skipped_videos"`
	TopicClusterCount int `json:"topic_cluster_count"`
	DecisionCount     int `json:"decision_count"`
	TodoCount         int `json:"todo_count"`
}

type TopicCluster struct {
	ClusterID      string           `json:"cluster_id"`
	Title          string           `json:"title"`
	BusinessObject string           `json:"business_object"`
	Summary        string           `json:"summary"`
	SourceVideoIDs []string         `json:"source_video_ids"`
	WorkItems      []WorkItem       `json:"work_items"`
	Evolution      []EvolutionEvent `json:"evolution"`
	Decisions      []Decision       `json:"decisions"`
	Todos          []Todo           `json:"todos"`
	Knowledge      []KnowledgeRef   `json:"knowledge"`
}

type WorkItem struct {
	ID                string        `json:"work_item_id"`
	Title             string        `json:"title"`
	Status            string        `json:"status"`
	CurrentConclusion string        `json:"current_conclusion,omitempty"`
	EvidenceRefs      []EvidenceRef `json:"evidence_refs"`
}
type EvolutionEvent struct {
	ID           string        `json:"id"`
	VideoID      string        `json:"video_id"`
	MeetingTitle string        `json:"meeting_title"`
	OccurredAt   *time.Time    `json:"occurred_at,omitempty"`
	Summary      string        `json:"summary"`
	Change       string        `json:"change"`
	EvidenceRefs []EvidenceRef `json:"evidence_refs"`
}
type Decision struct {
	ID           string        `json:"id"`
	Text         string        `json:"text"`
	VideoID      string        `json:"video_id"`
	Status       string        `json:"status,omitempty"`
	EvidenceRefs []EvidenceRef `json:"evidence_refs"`
}
type Todo struct {
	ID           string        `json:"id"`
	Title        string        `json:"title"`
	Owner        string        `json:"owner,omitempty"`
	Due          string        `json:"due,omitempty"`
	Status       string        `json:"status"`
	VideoID      string        `json:"video_id"`
	EvidenceRefs []EvidenceRef `json:"evidence_refs"`
}
type KnowledgeRef struct {
	KnowledgeObjectID string `json:"knowledge_object_id"`
	WikiPageID        string `json:"wiki_page_id"`
	KnowledgeType     string `json:"knowledge_type"`
	Title             string `json:"title"`
}
type EvidenceRef struct {
	VideoID              string `json:"video_id"`
	TranscriptGeneration string `json:"transcript_generation"`
	EvidenceID           string `json:"evidence_id"`
	StartMs              int    `json:"start_ms"`
	EndMs                int    `json:"end_ms"`
}
type TopicRelation struct {
	RelationID         string        `json:"relation_id"`
	SourceClusterID    string        `json:"source_cluster_id"`
	TargetClusterID    string        `json:"target_cluster_id"`
	RelationType       string        `json:"relation_type"`
	Summary            string        `json:"summary"`
	SourceEvidenceRefs []EvidenceRef `json:"source_evidence_refs"`
	TargetEvidenceRefs []EvidenceRef `json:"target_evidence_refs"`
	// EvidenceRefs is retained for reading projections created before the
	// two-sided evidence contract was introduced. New projections never emit it.
	EvidenceRefs []EvidenceRef `json:"evidence_refs,omitempty"`
}

// normalizeProjectionArrays keeps the wire contract deterministic. Go's nil
// slices otherwise serialize as null, while the meeting projection contract
// requires every collection to be an array, including empty collections.
func normalizeProjectionArrays(p *Projection) {
	if p == nil {
		return
	}
	if p.TopicClusters == nil {
		p.TopicClusters = []TopicCluster{}
	}
	if p.TopicRelations == nil {
		p.TopicRelations = []TopicRelation{}
	}
	for i := range p.TopicClusters {
		cluster := &p.TopicClusters[i]
		if cluster.SourceVideoIDs == nil {
			cluster.SourceVideoIDs = []string{}
		}
		if cluster.WorkItems == nil {
			cluster.WorkItems = []WorkItem{}
		}
		if cluster.Evolution == nil {
			cluster.Evolution = []EvolutionEvent{}
		}
		if cluster.Decisions == nil {
			cluster.Decisions = []Decision{}
		}
		if cluster.Todos == nil {
			cluster.Todos = []Todo{}
		}
		if cluster.Knowledge == nil {
			cluster.Knowledge = []KnowledgeRef{}
		}
		for j := range cluster.WorkItems {
			if cluster.WorkItems[j].EvidenceRefs == nil {
				cluster.WorkItems[j].EvidenceRefs = []EvidenceRef{}
			}
		}
		for j := range cluster.Evolution {
			if cluster.Evolution[j].EvidenceRefs == nil {
				cluster.Evolution[j].EvidenceRefs = []EvidenceRef{}
			}
		}
		for j := range cluster.Decisions {
			if cluster.Decisions[j].EvidenceRefs == nil {
				cluster.Decisions[j].EvidenceRefs = []EvidenceRef{}
			}
		}
		for j := range cluster.Todos {
			if cluster.Todos[j].EvidenceRefs == nil {
				cluster.Todos[j].EvidenceRefs = []EvidenceRef{}
			}
		}
	}
	for i := range p.TopicRelations {
		if p.TopicRelations[i].SourceEvidenceRefs == nil {
			p.TopicRelations[i].SourceEvidenceRefs = []EvidenceRef{}
		}
		if p.TopicRelations[i].TargetEvidenceRefs == nil {
			p.TopicRelations[i].TargetEvidenceRefs = []EvidenceRef{}
		}
	}
}

// Validate checks the publishable projection after model output has been
// materialized. It intentionally permits empty evidence on an empty/legacy
// fallback cluster, but every AI-derived fact must carry a valid locator.
func (p Projection) Validate() error {
	if p.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported projection schema")
	}
	clusterIDs := map[string]struct{}{}
	for _, cluster := range p.TopicClusters {
		if strings.TrimSpace(cluster.ClusterID) == "" || strings.TrimSpace(cluster.Title) == "" || strings.TrimSpace(cluster.BusinessObject) == "" {
			return fmt.Errorf("topic cluster identity is required")
		}
		if _, ok := clusterIDs[cluster.ClusterID]; ok {
			return fmt.Errorf("duplicate topic cluster id %q", cluster.ClusterID)
		}
		clusterIDs[cluster.ClusterID] = struct{}{}
		itemIDs := map[string]struct{}{}
		for _, item := range cluster.WorkItems {
			if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.Title) == "" {
				return fmt.Errorf("work item identity is required")
			}
			if _, ok := itemIDs[item.ID]; ok {
				return fmt.Errorf("duplicate work item id %q", item.ID)
			}
			itemIDs[item.ID] = struct{}{}
			if err := validateProjectionRefs(item.EvidenceRefs); err != nil {
				return err
			}
		}
		for _, event := range cluster.Evolution {
			if strings.TrimSpace(event.ID) == "" || strings.TrimSpace(event.VideoID) == "" {
				return fmt.Errorf("evolution identity is required")
			}
			if err := validateProjectionRefs(event.EvidenceRefs); err != nil {
				return err
			}
		}
		for _, decision := range cluster.Decisions {
			if strings.TrimSpace(decision.ID) == "" || strings.TrimSpace(decision.Text) == "" || strings.TrimSpace(decision.VideoID) == "" || len(decision.EvidenceRefs) == 0 {
				return fmt.Errorf("decision must have evidence")
			}
			if decision.Status != "" {
				if _, ok := map[string]struct{}{"replaced": {}, "invalidated": {}, "cancelled": {}}[decision.Status]; !ok {
					return fmt.Errorf("invalid decision status")
				}
			}
			if err := validateProjectionRefs(decision.EvidenceRefs); err != nil {
				return err
			}
		}
		for _, todo := range cluster.Todos {
			if strings.TrimSpace(todo.ID) == "" || strings.TrimSpace(todo.Title) == "" || strings.TrimSpace(todo.VideoID) == "" || len(todo.EvidenceRefs) == 0 {
				return fmt.Errorf("todo must have evidence")
			}
			if err := validateProjectionRefs(todo.EvidenceRefs); err != nil {
				return err
			}
		}
		for _, knowledge := range cluster.Knowledge {
			if strings.TrimSpace(knowledge.KnowledgeObjectID) == "" || strings.TrimSpace(knowledge.WikiPageID) == "" || !validKnowledgeType(knowledge.KnowledgeType) {
				return fmt.Errorf("invalid knowledge reference")
			}
		}
	}
	seenRelations := map[string]struct{}{}
	for _, relation := range p.TopicRelations {
		if _, ok := clusterIDs[relation.SourceClusterID]; !ok {
			return fmt.Errorf("relation source cluster is missing")
		}
		if _, ok := clusterIDs[relation.TargetClusterID]; !ok {
			return fmt.Errorf("relation target cluster is missing")
		}
		if relation.SourceClusterID == relation.TargetClusterID || strings.TrimSpace(relation.Summary) == "" {
			return fmt.Errorf("relation requires two-end evidence")
		}
		sourceRefs, targetRefs := relation.SourceEvidenceRefs, relation.TargetEvidenceRefs
		if len(sourceRefs) == 0 && len(targetRefs) == 0 && len(relation.EvidenceRefs) >= 2 {
			// Legacy data had no side marker; keep it readable until the next
			// successful regeneration writes the explicit form.
			sourceRefs, targetRefs = relation.EvidenceRefs[:1], relation.EvidenceRefs[1:]
		}
		if len(sourceRefs) == 0 || len(targetRefs) == 0 {
			return fmt.Errorf("relation requires two-end evidence")
		}
		if !isTopicRelationType(relation.RelationType) {
			return fmt.Errorf("invalid relation type")
		}
		if err := validateProjectionRefs(sourceRefs); err != nil {
			return err
		}
		if err := validateProjectionRefs(targetRefs); err != nil {
			return err
		}
		if len(relation.EvidenceRefs) > 0 {
			if err := validateProjectionRefs(relation.EvidenceRefs); err != nil {
				return err
			}
		}
		key := relation.SourceClusterID + "|" + relation.TargetClusterID + "|" + relation.RelationType
		if _, ok := seenRelations[key]; ok {
			return fmt.Errorf("duplicate topic relation")
		}
		seenRelations[key] = struct{}{}
	}
	return nil
}

func validateProjectionRefs(refs []EvidenceRef) error {
	seen := map[string]struct{}{}
	for _, ref := range refs {
		if strings.TrimSpace(ref.VideoID) == "" || strings.TrimSpace(ref.TranscriptGeneration) == "" || strings.TrimSpace(ref.EvidenceID) == "" || ref.StartMs < 0 || ref.EndMs <= ref.StartMs {
			return fmt.Errorf("invalid evidence reference")
		}
		if _, ok := seen[ref.EvidenceID]; ok {
			return fmt.Errorf("duplicate evidence reference")
		}
		seen[ref.EvidenceID] = struct{}{}
	}
	return nil
}
func validKnowledgeType(value string) bool {
	_, ok := map[string]struct{}{"entity": {}, "concept": {}, "case": {}, "methodology": {}, "insight": {}}[strings.TrimSpace(value)]
	return ok
}

func isTopicRelationType(value string) bool {
	switch strings.TrimSpace(value) {
	case RelationPrerequisite, RelationConflictConstraint, RelationSharedSupport, RelationResultFeedback:
		return true
	default:
		return false
	}
}
