package wikiaudit

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
)

const (
	InputModeFullDocument = "full_document"
	ProducerNativeWiki    = "weknora_native"
	ScopeVideo            = "video"
	ScopeKnowledgeBase    = "knowledge_base"
)

// SourceIdentity is the non-content identity carried by a standardized video
// document. It is safe to include in audit logs.
type SourceIdentity struct {
	VideoID              string
	TranscriptGeneration string
	SourceKnowledgeID    string
	KnowledgeBaseID      string
}

// Event is the P2 native Wiki audit contract. Required fields intentionally do
// not use omitempty so malformed evidence is visible rather than ambiguous.
type Event struct {
	EventID               string   `json:"event_id"`
	EventScope            string   `json:"event_scope"`
	RunID                 string   `json:"run_id"`
	VideoID               string   `json:"video_id"`
	TranscriptGeneration  string   `json:"transcript_generation"`
	SourceKnowledgeID     string   `json:"source_knowledge_id"`
	KnowledgeBaseID       string   `json:"knowledge_base_id"`
	InputMode             string   `json:"input_mode"`
	PageProducer          string   `json:"page_producer"`
	TaskID                string   `json:"task_id"`
	EventTime             string   `json:"event_time"`
	TaskType              string   `json:"task_type"`
	Op                    string   `json:"op"`
	PendingOpID           string   `json:"pending_op_id"`
	Phase                 string   `json:"phase"`
	Status                string   `json:"status"`
	CandidateCount        *int     `json:"candidate_count,omitempty"`
	InputCount            *int     `json:"input_count,omitempty"`
	OutputCount           *int     `json:"output_count,omitempty"`
	PageID                string   `json:"page_id,omitempty"`
	Slug                  string   `json:"slug,omitempty"`
	PageType              string   `json:"page_type,omitempty"`
	Version               *int     `json:"version,omitempty"`
	IndexPageID           string   `json:"index_page_id,omitempty"`
	RelatedIngestEventIDs []string `json:"related_ingest_event_ids,omitempty"`
	Reason                string   `json:"reason,omitempty"`
}

func RunID(identity SourceIdentity) string {
	key := strings.Join([]string{
		strings.TrimSpace(identity.VideoID),
		strings.TrimSpace(identity.TranscriptGeneration),
		strings.TrimSpace(identity.SourceKnowledgeID),
		strings.TrimSpace(identity.KnowledgeBaseID),
	}, "|")
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(key)).String()
}

func New(identity SourceIdentity, taskID, taskType, op, pendingOpID, phase, status string) Event {
	return Event{
		EventID: uuid.NewString(), EventScope: ScopeVideo, RunID: RunID(identity),
		VideoID: identity.VideoID, TranscriptGeneration: identity.TranscriptGeneration,
		SourceKnowledgeID: identity.SourceKnowledgeID, KnowledgeBaseID: identity.KnowledgeBaseID,
		InputMode: InputModeFullDocument, PageProducer: ProducerNativeWiki,
		TaskID: taskID, EventTime: time.Now().UTC().Format(time.RFC3339Nano),
		TaskType: taskType, Op: op, PendingOpID: pendingOpID, Phase: phase, Status: status,
	}
}

func NewFinalize(kbID, taskID, pendingOpID, runID, indexPageID string, related []string, inputCount, outputCount int) Event {
	event := New(SourceIdentity{
		VideoID: "not_applicable", TranscriptGeneration: "not_applicable",
		SourceKnowledgeID: "not_applicable", KnowledgeBaseID: kbID,
	}, taskID, "wiki:finalize", "finalize", pendingOpID, "finalize", "succeeded")
	event.EventScope = ScopeKnowledgeBase
	event.RunID = runID
	event.IndexPageID = indexPageID
	event.RelatedIngestEventIDs = append([]string(nil), related...)
	event.InputCount = intPtr(inputCount)
	event.OutputCount = intPtr(outputCount)
	return event
}

func (e Event) Validate() error {
	required := map[string]string{
		"event_id": e.EventID, "event_scope": e.EventScope, "run_id": e.RunID,
		"video_id": e.VideoID, "transcript_generation": e.TranscriptGeneration,
		"source_knowledge_id": e.SourceKnowledgeID, "knowledge_base_id": e.KnowledgeBaseID,
		"input_mode": e.InputMode, "page_producer": e.PageProducer, "task_id": e.TaskID,
		"event_time": e.EventTime, "task_type": e.TaskType, "op": e.Op,
		"pending_op_id": e.PendingOpID, "phase": e.Phase, "status": e.Status,
	}
	for name, value := range required {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("wiki_audit_event:%s_missing", name)
		}
	}
	switch e.Phase {
	case "map":
		if e.CandidateCount == nil {
			return fmt.Errorf("wiki_audit_event:candidate_count_missing")
		}
	case "reduce", "publish", "finalize":
		if e.InputCount == nil || e.OutputCount == nil {
			return fmt.Errorf("wiki_audit_event:stage_counts_missing")
		}
	case "page_write":
		if e.PageID == "" || e.Slug == "" || e.PageType == "" || e.Version == nil {
			return fmt.Errorf("wiki_audit_event:page_identity_missing")
		}
	}
	if e.EventScope == ScopeKnowledgeBase {
		if e.SourceKnowledgeID != "not_applicable" || e.IndexPageID == "" || len(e.RelatedIngestEventIDs) == 0 {
			return fmt.Errorf("wiki_audit_event:finalize_identity_invalid")
		}
	}
	return nil
}

func (e Event) JSON() (string, error) {
	if err := e.Validate(); err != nil {
		return "", err
	}
	raw, err := json.Marshal(e)
	return string(raw), err
}

func ParseSourceIdentity(content, sourceKnowledgeID, kbID string) (SourceIdentity, error) {
	identity := SourceIdentity{SourceKnowledgeID: strings.TrimSpace(sourceKnowledgeID), KnowledgeBaseID: strings.TrimSpace(kbID)}
	trimmed := strings.TrimSpace(content)
	if !strings.HasPrefix(trimmed, "---\n") {
		return identity, fmt.Errorf("wiki_audit_event:source_frontmatter_missing")
	}
	rest := strings.TrimPrefix(trimmed, "---\n")
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return identity, fmt.Errorf("wiki_audit_event:source_frontmatter_unclosed")
	}
	var frontmatter struct {
		Type                 string `yaml:"type"`
		SourceVideoID        string `yaml:"source_video_id"`
		TranscriptGeneration string `yaml:"transcript_generation"`
	}
	if err := yaml.Unmarshal([]byte(rest[:end]), &frontmatter); err != nil {
		return identity, fmt.Errorf("wiki_audit_event:source_frontmatter_invalid: %w", err)
	}
	if frontmatter.Type != "video_transcript_source" || strings.TrimSpace(frontmatter.SourceVideoID) == "" || strings.TrimSpace(frontmatter.TranscriptGeneration) == "" {
		return identity, fmt.Errorf("wiki_audit_event:source_identity_invalid")
	}
	identity.VideoID = strings.TrimSpace(frontmatter.SourceVideoID)
	identity.TranscriptGeneration = strings.TrimSpace(frontmatter.TranscriptGeneration)
	return identity, nil
}

func Count(value int) *int { return intPtr(value) }

func intPtr(value int) *int { return &value }
