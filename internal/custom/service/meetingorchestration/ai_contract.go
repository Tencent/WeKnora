package meetingorchestration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type ItemCandidateOutput struct {
	Candidates []ItemCandidate `json:"candidates"`
}
type ItemCandidate struct {
	CandidateRefs            []string       `json:"candidate_refs"`
	BusinessObject           BusinessObject `json:"business_object"`
	SpecificQuestion         string         `json:"specific_question"`
	WorkItemScope            *string        `json:"work_item_scope"`
	WorkItemScopeEvidenceIDs []string       `json:"work_item_scope_evidence_ids"`
	CycleSignal              *string        `json:"cycle_signal"`
	CycleSignalEvidenceIDs   []string       `json:"cycle_signal_evidence_ids"`
	CoreLevel                string         `json:"core_level"`
	Reason                   string         `json:"reason"`
	EvidenceIDs              []string       `json:"evidence_ids"`
}
type BusinessObject struct {
	Name        string   `json:"name"`
	Scope       string   `json:"scope"`
	Aliases     []string `json:"aliases"`
	EvidenceIDs []string `json:"evidence_ids"`
}
type TopicMatchOutput struct {
	Matches []TopicMatch `json:"matches"`
}
type TopicMatch struct {
	CandidateID             string   `json:"candidate_id"`
	ObjectDecision          string   `json:"object_decision"`
	MatchedTopicID          *string  `json:"matched_topic_id"`
	Reason                  string   `json:"reason"`
	CandidateEvidenceIDs    []string `json:"candidate_evidence_ids"`
	MatchedTopicEvidenceIDs []string `json:"matched_topic_evidence_ids"`
}
type WorkItemMatchOutput struct {
	Matches []WorkItemMatch `json:"matches"`
}
type WorkItemMatch struct {
	CandidateID                string   `json:"candidate_id"`
	WorkItemDecision           string   `json:"work_item_decision"`
	MatchedWorkItemID          *string  `json:"matched_work_item_id"`
	Reason                     string   `json:"reason"`
	CandidateEvidenceIDs       []string `json:"candidate_evidence_ids"`
	MatchedWorkItemEvidenceIDs []string `json:"matched_work_item_evidence_ids"`
}

type WorkItemUpdateOutput struct {
	WorkItemUpdates []WorkItemUpdate `json:"work_item_updates"`
}

type WorkItemUpdate struct {
	CandidateID                  string                 `json:"candidate_id"`
	TopicID                      string                 `json:"topic_id"`
	WorkItemRef                  string                 `json:"work_item_ref"`
	CurrentStatus                *string                `json:"current_status"`
	CurrentStatusEvidenceIDs     []string               `json:"current_status_evidence_ids"`
	CurrentConclusion            *string                `json:"current_conclusion"`
	CurrentConclusionEvidenceIDs []string               `json:"current_conclusion_evidence_ids"`
	Change                       Change                 `json:"change"`
	ImportantDecisions           []ImportantDecision    `json:"important_decisions"`
	DecisionEffectUpdates        []DecisionEffectUpdate `json:"decision_effect_updates"`
	Todos                        []UpdateTodo           `json:"todos"`
}

type Change struct {
	IsSubstantive       bool     `json:"is_substantive"`
	ChangeType          *string  `json:"change_type"`
	Discussion          *string  `json:"discussion"`
	Conclusion          *string  `json:"conclusion"`
	ChangeFromPrevious  *string  `json:"change_from_previous"`
	CurrentProgress     *string  `json:"current_progress"`
	EvidenceIDs         []string `json:"evidence_ids"`
	PreviousEvidenceIDs []string `json:"previous_evidence_ids"`
	CurrentEvidenceIDs  []string `json:"current_evidence_ids"`
}

type ImportantDecision struct {
	Content            string   `json:"content"`
	DecisionType       string   `json:"decision_type"`
	ReplacesDecisionID *string  `json:"replaces_decision_id"`
	EvidenceIDs        []string `json:"evidence_ids"`
}

type DecisionEffectUpdate struct {
	DecisionID             string   `json:"decision_id"`
	EffectStatusSuggestion string   `json:"effect_status_suggestion"`
	EffectEvidenceIDs      []string `json:"effect_evidence_ids"`
}

type UpdateTodo struct {
	TodoMatchDecision    string   `json:"todo_match_decision"`
	MatchedTodoID        *string  `json:"matched_todo_id"`
	TodoType             string   `json:"todo_type"`
	Content              string   `json:"content"`
	Owner                *string  `json:"owner"`
	DueText              *string  `json:"due_text"`
	Priority             *string  `json:"priority"`
	TodoStatusSuggestion *string  `json:"todo_status_suggestion"`
	StatusEvidenceIDs    []string `json:"status_evidence_ids"`
	EvidenceIDs          []string `json:"evidence_ids"`
}

type KnowledgeSelectionOutput struct {
	KnowledgeRefs []KnowledgeSelection `json:"knowledge_refs"`
}

type KnowledgeSelection struct {
	TopicID           string   `json:"topic_id"`
	KnowledgeObjectID string   `json:"knowledge_object_id"`
	RelationSummary   string   `json:"relation_summary"`
	EvidenceIDs       []string `json:"evidence_ids"`
}

type TopicRelationOutput struct {
	Relations []TopicRelationCandidate `json:"relations"`
}

type TopicRelationCandidate struct {
	SourceTopicID     string   `json:"source_topic_id"`
	TargetTopicID     string   `json:"target_topic_id"`
	RelationType      string   `json:"relation_type"`
	Summary           string   `json:"summary"`
	SourceEvidenceIDs []string `json:"source_evidence_ids"`
	TargetEvidenceIDs []string `json:"target_evidence_ids"`
}

func decodeStrict(raw string, target any) error {
	decoder := json.NewDecoder(bytes.NewReader([]byte(strings.TrimSpace(raw))))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid meeting model JSON: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("invalid meeting model JSON: trailing content")
	}
	return nil
}

func validateEvidenceIDs(ids []string, allowed map[string]struct{}, required bool) error {
	if required && len(ids) == 0 {
		return fmt.Errorf("evidence_ids must not be empty")
	}
	seen := map[string]struct{}{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			return fmt.Errorf("evidence id is empty")
		}
		if _, ok := seen[id]; ok {
			return fmt.Errorf("duplicate evidence id %q", id)
		}
		seen[id] = struct{}{}
		if _, ok := allowed[id]; !ok {
			return fmt.Errorf("evidence id %q is not in whitelist", id)
		}
	}
	return nil
}

func (o ItemCandidateOutput) Validate(allowed map[string]struct{}) error {
	if len(o.Candidates) > 20 {
		return fmt.Errorf("too many candidates")
	}
	for _, c := range o.Candidates {
		if strings.TrimSpace(c.CoreLevel) != "core" && strings.TrimSpace(c.CoreLevel) != "secondary" {
			return fmt.Errorf("invalid core_level")
		}
		if c.WorkItemScope == nil && len(c.WorkItemScopeEvidenceIDs) > 0 {
			return fmt.Errorf("scope evidence requires scope")
		}
		if c.CycleSignal == nil && len(c.CycleSignalEvidenceIDs) > 0 {
			return fmt.Errorf("cycle evidence requires cycle signal")
		}
		if err := validateEvidenceIDs(c.EvidenceIDs, allowed, true); err != nil {
			return err
		}
		if err := validateEvidenceIDs(c.BusinessObject.EvidenceIDs, allowed, true); err != nil {
			return err
		}
		if err := validateEvidenceIDs(c.WorkItemScopeEvidenceIDs, allowed, c.WorkItemScope != nil); err != nil {
			return err
		}
		if err := validateEvidenceIDs(c.CycleSignalEvidenceIDs, allowed, c.CycleSignal != nil); err != nil {
			return err
		}
	}
	return nil
}

func (o TopicMatchOutput) Validate(allowed map[string]struct{}) error {
	if len(o.Matches) != 1 {
		return fmt.Errorf("topic match must contain exactly one item")
	}
	for _, m := range o.Matches {
		if m.ObjectDecision != "same_object" && m.ObjectDecision != "possible" && m.ObjectDecision != "new_object" {
			return fmt.Errorf("invalid object_decision")
		}
		if err := validateEvidenceIDs(m.CandidateEvidenceIDs, allowed, true); err != nil {
			return err
		}
		if m.ObjectDecision == "same_object" {
			if m.MatchedTopicID == nil || len(m.MatchedTopicEvidenceIDs) == 0 {
				return fmt.Errorf("same_object requires matched topic evidence")
			}
		} else if m.MatchedTopicID != nil || len(m.MatchedTopicEvidenceIDs) > 0 {
			return fmt.Errorf("new object cannot contain matched topic")
		}
	}
	return nil
}

func (o WorkItemMatchOutput) Validate(allowed map[string]struct{}) error {
	if len(o.Matches) != 1 {
		return fmt.Errorf("work item match must contain exactly one item")
	}
	for _, m := range o.Matches {
		if m.WorkItemDecision != "same_item" && m.WorkItemDecision != "possible" && m.WorkItemDecision != "new_item" {
			return fmt.Errorf("invalid work_item_decision")
		}
		if err := validateEvidenceIDs(m.CandidateEvidenceIDs, allowed, true); err != nil {
			return err
		}
		if m.WorkItemDecision == "same_item" {
			if m.MatchedWorkItemID == nil || len(m.MatchedWorkItemEvidenceIDs) == 0 {
				return fmt.Errorf("same_item requires matched work item evidence")
			}
		} else if m.MatchedWorkItemID != nil || len(m.MatchedWorkItemEvidenceIDs) > 0 {
			return fmt.Errorf("new item cannot contain matched work item")
		}
	}
	return nil
}

func (o WorkItemUpdateOutput) Validate(allowed map[string]struct{}) error {
	if len(o.WorkItemUpdates) != 1 {
		return fmt.Errorf("work_item_updates must contain exactly one item")
	}
	u := o.WorkItemUpdates[0]
	if strings.TrimSpace(u.CandidateID) == "" || strings.TrimSpace(u.TopicID) == "" || strings.TrimSpace(u.WorkItemRef) == "" {
		return fmt.Errorf("work item update identities are required")
	}
	statuses := map[string]struct{}{"pending_clarification": {}, "option_available": {}, "under_review": {}, "decided": {}, "in_progress": {}, "verified": {}, "on_hold": {}, "cancelled": {}}
	if u.CurrentStatus == nil {
		if len(u.CurrentStatusEvidenceIDs) > 0 {
			return fmt.Errorf("status evidence requires status")
		}
	} else {
		if _, ok := statuses[*u.CurrentStatus]; !ok {
			return fmt.Errorf("invalid current_status")
		}
		if err := validateEvidenceIDs(u.CurrentStatusEvidenceIDs, allowed, true); err != nil {
			return err
		}
	}
	if u.CurrentConclusion == nil {
		if len(u.CurrentConclusionEvidenceIDs) > 0 {
			return fmt.Errorf("conclusion evidence requires conclusion")
		}
	} else if err := validateEvidenceIDs(u.CurrentConclusionEvidenceIDs, allowed, true); err != nil {
		return err
	}
	if err := u.Change.Validate(allowed); err != nil {
		return err
	}
	if len(u.ImportantDecisions) > 20 || len(u.DecisionEffectUpdates) > 20 || len(u.Todos) > 20 {
		return fmt.Errorf("work item update array exceeds limit")
	}
	for _, d := range u.ImportantDecisions {
		if strings.TrimSpace(d.Content) == "" {
			return fmt.Errorf("decision content is required")
		}
		if d.DecisionType != "confirmed" && d.DecisionType != "rejected" && d.DecisionType != "replaced" {
			return fmt.Errorf("invalid decision_type")
		}
		if d.DecisionType == "replaced" && (d.ReplacesDecisionID == nil || strings.TrimSpace(*d.ReplacesDecisionID) == "") {
			return fmt.Errorf("replaced decision requires old decision")
		}
		if d.DecisionType != "replaced" && d.ReplacesDecisionID != nil {
			return fmt.Errorf("only replaced decision may reference old decision")
		}
		if err := validateEvidenceIDs(d.EvidenceIDs, allowed, true); err != nil {
			return err
		}
	}
	seenEffects := map[string]struct{}{}
	for _, e := range u.DecisionEffectUpdates {
		if strings.TrimSpace(e.DecisionID) == "" {
			return fmt.Errorf("decision effect id is required")
		}
		if e.EffectStatusSuggestion != "replaced" && e.EffectStatusSuggestion != "invalidated" && e.EffectStatusSuggestion != "cancelled" {
			return fmt.Errorf("invalid effect_status_suggestion")
		}
		if _, ok := seenEffects[e.DecisionID]; ok {
			return fmt.Errorf("duplicate decision effect")
		}
		seenEffects[e.DecisionID] = struct{}{}
		if err := validateEvidenceIDs(e.EffectEvidenceIDs, allowed, true); err != nil {
			return err
		}
	}
	for _, todo := range u.Todos {
		if todo.TodoMatchDecision != "same_todo" && todo.TodoMatchDecision != "possible" && todo.TodoMatchDecision != "new_todo" {
			return fmt.Errorf("invalid todo_match_decision")
		}
		if todo.TodoType != "pending_decision" && todo.TodoType != "action" {
			return fmt.Errorf("invalid todo_type")
		}
		if strings.TrimSpace(todo.Content) == "" {
			return fmt.Errorf("todo content is required")
		}
		if todo.TodoMatchDecision == "same_todo" {
			if todo.MatchedTodoID == nil || strings.TrimSpace(*todo.MatchedTodoID) == "" {
				return fmt.Errorf("same_todo requires matched id")
			}
		} else if todo.MatchedTodoID != nil || todo.TodoStatusSuggestion != nil || len(todo.StatusEvidenceIDs) > 0 {
			return fmt.Errorf("new or possible todo cannot update status")
		}
		if todo.TodoStatusSuggestion != nil {
			valid := map[string]struct{}{"pending": {}, "in_progress": {}, "completed": {}, "cancelled": {}, "replaced": {}}
			if _, ok := valid[*todo.TodoStatusSuggestion]; !ok {
				return fmt.Errorf("invalid todo status")
			}
			if err := validateEvidenceIDs(todo.StatusEvidenceIDs, allowed, true); err != nil {
				return err
			}
		} else if len(todo.StatusEvidenceIDs) > 0 {
			return fmt.Errorf("status evidence requires status")
		}
		if todo.Priority != nil {
			if _, ok := map[string]struct{}{"high": {}, "medium": {}, "low": {}}[*todo.Priority]; !ok {
				return fmt.Errorf("invalid todo priority")
			}
		}
		if err := validateEvidenceIDs(todo.EvidenceIDs, allowed, true); err != nil {
			return err
		}
	}
	return nil
}

func (c Change) Validate(allowed map[string]struct{}) error {
	if !c.IsSubstantive {
		if c.ChangeType != nil || c.Discussion != nil || c.Conclusion != nil || c.ChangeFromPrevious != nil || c.CurrentProgress != nil || len(c.EvidenceIDs) > 0 || len(c.PreviousEvidenceIDs) > 0 || len(c.CurrentEvidenceIDs) > 0 {
			return fmt.Errorf("non-substantive change must be empty")
		}
		return nil
	}
	if c.ChangeType == nil {
		return fmt.Errorf("substantive change requires change_type")
	}
	valid := map[string]struct{}{"added": {}, "supplemented": {}, "adjusted": {}, "confirmed": {}, "replaced": {}, "verified": {}}
	if _, ok := valid[*c.ChangeType]; !ok {
		return fmt.Errorf("invalid change_type")
	}
	if c.Discussion == nil || strings.TrimSpace(*c.Discussion) == "" {
		return fmt.Errorf("substantive change requires discussion")
	}
	if *c.ChangeType == "added" {
		if c.ChangeFromPrevious != nil || len(c.PreviousEvidenceIDs) > 0 {
			return fmt.Errorf("added change cannot have previous evidence")
		}
	} else {
		if c.ChangeFromPrevious == nil || strings.TrimSpace(*c.ChangeFromPrevious) == "" {
			return fmt.Errorf("change requires previous description")
		}
		if err := validateEvidenceIDs(c.PreviousEvidenceIDs, allowed, true); err != nil {
			return err
		}
	}
	if err := validateEvidenceIDs(c.EvidenceIDs, allowed, true); err != nil {
		return err
	}
	return validateEvidenceIDs(c.CurrentEvidenceIDs, allowed, true)
}

func (o KnowledgeSelectionOutput) Validate(allowed map[string]struct{}) error {
	if len(o.KnowledgeRefs) > 20 {
		return fmt.Errorf("too many knowledge refs")
	}
	seen := map[string]struct{}{}
	for _, ref := range o.KnowledgeRefs {
		if strings.TrimSpace(ref.TopicID) == "" || strings.TrimSpace(ref.KnowledgeObjectID) == "" || strings.TrimSpace(ref.RelationSummary) == "" {
			return fmt.Errorf("knowledge ref fields are required")
		}
		if _, ok := seen[ref.KnowledgeObjectID]; ok {
			return fmt.Errorf("duplicate knowledge object")
		}
		seen[ref.KnowledgeObjectID] = struct{}{}
		if err := validateEvidenceIDs(ref.EvidenceIDs, allowed, true); err != nil {
			return err
		}
	}
	return nil
}

func (o TopicRelationOutput) Validate(allowed map[string]struct{}) error {
	if len(o.Relations) > 4 {
		return fmt.Errorf("too many topic relations")
	}
	for _, r := range o.Relations {
		if strings.TrimSpace(r.SourceTopicID) == "" || strings.TrimSpace(r.TargetTopicID) == "" || r.SourceTopicID == r.TargetTopicID || strings.TrimSpace(r.Summary) == "" {
			return fmt.Errorf("invalid topic relation endpoints")
		}
		if !isTopicRelationType(r.RelationType) {
			return fmt.Errorf("invalid relation_type")
		}
		if err := validateEvidenceIDs(r.SourceEvidenceIDs, allowed, true); err != nil {
			return err
		}
		if err := validateEvidenceIDs(r.TargetEvidenceIDs, allowed, true); err != nil {
			return err
		}
	}
	return nil
}
