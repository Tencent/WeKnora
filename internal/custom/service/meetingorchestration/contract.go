package meetingorchestration

import (
	"fmt"
	"strings"
	"time"
)

const SchemaVersion = "meeting-orchestration/v2"
const LegacySchemaVersion = "meeting-orchestration/v1"

const (
	RelationPrerequisite       = "prerequisite"
	RelationConflictConstraint = "conflict_constraint"
	RelationSharedSupport      = "shared_support"
	RelationResultFeedback     = "result_feedback"
)

type Projection struct {
	SchemaVersion     string           `json:"schema_version"`
	OwnerScopeID      string           `json:"owner_scope_id"`
	SourceFingerprint string           `json:"source_fingerprint"`
	GeneratedAt       time.Time        `json:"generated_at"`
	Statistics        Statistics       `json:"statistics"`
	MeetingSessions   []MeetingSession `json:"meeting_sessions"`
	TopicClusters     []TopicCluster   `json:"topic_clusters"`
	TopicRelations    []TopicRelation  `json:"topic_cluster_relations"`
}

type Statistics struct {
	ScannedVideos       int `json:"scanned_videos"`
	QualifiedVideos     int `json:"qualified_videos"`
	SkippedVideos       int `json:"skipped_videos"`
	TopicClusterCount   int `json:"topic_cluster_count"`
	DecisionCount       int `json:"decision_count"`
	TodoCount           int `json:"todo_count"`
	MeetingSessionCount int `json:"meeting_session_count"`
	PossiblePairCount   int `json:"possible_pair_count"`
}

type MeetingSession struct {
	MeetingSessionID     string        `json:"meeting_session_id"`
	FragmentVideoIDs     []string      `json:"fragment_video_ids"`
	OrderingBasis        string        `json:"ordering_basis"`
	GroupingEvidenceRefs []EvidenceRef `json:"grouping_evidence_refs"`
}

type TopicCluster struct {
	ClusterID          string              `json:"cluster_id"`
	Title              string              `json:"title"`
	BusinessObject     string              `json:"business_object"`
	Summary            string              `json:"summary"`
	SourceVideoIDs     []string            `json:"source_video_ids"`
	MeetingSessionIDs  []string            `json:"meeting_session_ids"`
	VideoContributions []VideoContribution `json:"video_contributions"`
	WorkItems          []WorkItem          `json:"work_items"`
	Evolution          []EvolutionEvent    `json:"evolution"`
	Decisions          []Decision          `json:"decisions"`
	Todos              []Todo              `json:"todos"`
	Knowledge          []KnowledgeRef      `json:"knowledge"`
}

type VideoContribution struct {
	VideoID          string        `json:"video_id"`
	MeetingSessionID string        `json:"meeting_session_id"`
	ContributionType string        `json:"contribution_type"`
	EvidenceRefs     []EvidenceRef `json:"evidence_refs"`
}

type WorkItem struct {
	ID                string        `json:"work_item_id"`
	Title             string        `json:"title"`
	Status            string        `json:"status"`
	CurrentConclusion string        `json:"current_conclusion,omitempty"`
	EvidenceRefs      []EvidenceRef `json:"evidence_refs"`
}
type EvolutionEvent struct {
	ID               string        `json:"id"`
	VideoID          string        `json:"video_id"`
	MeetingSessionID string        `json:"meeting_session_id,omitempty"`
	WorkItemID       string        `json:"work_item_id,omitempty"`
	MeetingTitle     string        `json:"meeting_title"`
	OccurredAt       *time.Time    `json:"occurred_at,omitempty"`
	Summary          string        `json:"summary"`
	Change           string        `json:"change"`
	ChangeType       string        `json:"change_type,omitempty"`
	PreviousState    string        `json:"previous_state,omitempty"`
	NextState        string        `json:"next_state,omitempty"`
	EvidenceRefs     []EvidenceRef `json:"evidence_refs"`
}
type Decision struct {
	ID               string        `json:"id"`
	Text             string        `json:"text"`
	VideoID          string        `json:"video_id"`
	MeetingSessionID string        `json:"meeting_session_id,omitempty"`
	Status           string        `json:"status,omitempty"`
	EvidenceRefs     []EvidenceRef `json:"evidence_refs"`
}
type Todo struct {
	ID               string        `json:"id"`
	Title            string        `json:"title"`
	Owner            string        `json:"owner,omitempty"`
	Due              string        `json:"due,omitempty"`
	Status           string        `json:"status"`
	VideoID          string        `json:"video_id"`
	MeetingSessionID string        `json:"meeting_session_id,omitempty"`
	EvidenceRefs     []EvidenceRef `json:"evidence_refs"`
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
	if p.MeetingSessions == nil {
		p.MeetingSessions = []MeetingSession{}
	}
	for i := range p.MeetingSessions {
		if p.MeetingSessions[i].FragmentVideoIDs == nil {
			p.MeetingSessions[i].FragmentVideoIDs = []string{}
		}
		if p.MeetingSessions[i].GroupingEvidenceRefs == nil {
			p.MeetingSessions[i].GroupingEvidenceRefs = []EvidenceRef{}
		}
	}
	if p.TopicRelations == nil {
		p.TopicRelations = []TopicRelation{}
	}
	for i := range p.TopicClusters {
		cluster := &p.TopicClusters[i]
		if cluster.SourceVideoIDs == nil {
			cluster.SourceVideoIDs = []string{}
		}
		if cluster.MeetingSessionIDs == nil {
			cluster.MeetingSessionIDs = []string{}
		}
		if cluster.VideoContributions == nil {
			cluster.VideoContributions = []VideoContribution{}
		}
		for j := range cluster.VideoContributions {
			if cluster.VideoContributions[j].EvidenceRefs == nil {
				cluster.VideoContributions[j].EvidenceRefs = []EvidenceRef{}
			}
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
	if p.SchemaVersion != SchemaVersion && p.SchemaVersion != LegacySchemaVersion {
		return fmt.Errorf("unsupported projection schema")
	}
	sessionIDs := map[string]struct{}{}
	videoSessions := map[string]string{}
	for _, session := range p.MeetingSessions {
		if strings.TrimSpace(session.MeetingSessionID) == "" {
			return fmt.Errorf("meeting session identity is required")
		}
		if _, exists := sessionIDs[session.MeetingSessionID]; exists {
			return fmt.Errorf("duplicate meeting session id %q", session.MeetingSessionID)
		}
		sessionIDs[session.MeetingSessionID] = struct{}{}
		if len(session.FragmentVideoIDs) == 0 {
			return fmt.Errorf("meeting session requires fragments")
		}
		for _, videoID := range session.FragmentVideoIDs {
			videoID = strings.TrimSpace(videoID)
			if videoID == "" {
				return fmt.Errorf("meeting session contains empty fragment")
			}
			if previous, exists := videoSessions[videoID]; exists {
				return fmt.Errorf("video %q belongs to multiple meeting sessions %q and %q", videoID, previous, session.MeetingSessionID)
			}
			videoSessions[videoID] = session.MeetingSessionID
		}
		if err := validateProjectionRefs(session.GroupingEvidenceRefs); err != nil {
			return err
		}
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
		clusterSessionIDs := map[string]struct{}{}
		for _, sessionID := range cluster.MeetingSessionIDs {
			sessionID = strings.TrimSpace(sessionID)
			if p.SchemaVersion == SchemaVersion {
				if _, ok := sessionIDs[sessionID]; !ok {
					return fmt.Errorf("topic cluster references missing meeting session %q", sessionID)
				}
				if _, duplicate := clusterSessionIDs[sessionID]; duplicate {
					return fmt.Errorf("topic cluster repeats meeting session %q", sessionID)
				}
				clusterSessionIDs[sessionID] = struct{}{}
			}
		}
		contributionVideoIDs := map[string]struct{}{}
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
			if p.SchemaVersion == SchemaVersion && event.MeetingSessionID != "" {
				if sessionID, ok := videoSessions[event.VideoID]; !ok || sessionID != event.MeetingSessionID {
					return fmt.Errorf("evolution event %q does not belong to meeting session", event.ID)
				}
			}
		}
		for _, contribution := range cluster.VideoContributions {
			if strings.TrimSpace(contribution.VideoID) == "" || strings.TrimSpace(contribution.MeetingSessionID) == "" || strings.TrimSpace(contribution.ContributionType) == "" {
				return fmt.Errorf("video contribution identity is required")
			}
			if err := validateProjectionRefs(contribution.EvidenceRefs); err != nil {
				return err
			}
			if p.SchemaVersion == SchemaVersion {
				if _, duplicate := contributionVideoIDs[contribution.VideoID]; duplicate {
					return fmt.Errorf("topic cluster repeats video contribution %q", contribution.VideoID)
				}
				contributionVideoIDs[contribution.VideoID] = struct{}{}
				if sessionID, ok := videoSessions[contribution.VideoID]; !ok || sessionID != contribution.MeetingSessionID {
					return fmt.Errorf("video contribution %q does not belong to meeting session %q", contribution.VideoID, contribution.MeetingSessionID)
				}
				if _, ok := clusterSessionIDs[contribution.MeetingSessionID]; !ok {
					return fmt.Errorf("video contribution %q uses an unlisted meeting session", contribution.VideoID)
				}
			}
		}
		if p.SchemaVersion == SchemaVersion {
			sourceVideoIDs := map[string]struct{}{}
			for _, videoID := range cluster.SourceVideoIDs {
				videoID = strings.TrimSpace(videoID)
				if videoID == "" {
					return fmt.Errorf("topic cluster contains empty source video")
				}
				if _, duplicate := sourceVideoIDs[videoID]; duplicate {
					return fmt.Errorf("topic cluster repeats source video %q", videoID)
				}
				sourceVideoIDs[videoID] = struct{}{}
			}
			if len(sourceVideoIDs) != len(contributionVideoIDs) {
				return fmt.Errorf("topic cluster source videos and contributions differ")
			}
			for videoID := range sourceVideoIDs {
				if _, ok := contributionVideoIDs[videoID]; !ok {
					return fmt.Errorf("topic cluster source video %q has no contribution", videoID)
				}
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
			if p.SchemaVersion == SchemaVersion && decision.MeetingSessionID != "" {
				if sessionID, ok := videoSessions[decision.VideoID]; !ok || sessionID != decision.MeetingSessionID {
					return fmt.Errorf("decision %q does not belong to meeting session", decision.ID)
				}
			}
		}
		for _, todo := range cluster.Todos {
			if strings.TrimSpace(todo.ID) == "" || strings.TrimSpace(todo.Title) == "" || strings.TrimSpace(todo.VideoID) == "" || len(todo.EvidenceRefs) == 0 {
				return fmt.Errorf("todo must have evidence")
			}
			if err := validateProjectionRefs(todo.EvidenceRefs); err != nil {
				return err
			}
			if p.SchemaVersion == SchemaVersion && todo.MeetingSessionID != "" {
				if sessionID, ok := videoSessions[todo.VideoID]; !ok || sessionID != todo.MeetingSessionID {
					return fmt.Errorf("todo %q does not belong to meeting session", todo.ID)
				}
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
