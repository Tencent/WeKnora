package meetingorchestration

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/custom/client/weknora"
	"github.com/Tencent/WeKnora/internal/custom/model"
	"github.com/Tencent/WeKnora/internal/custom/service/promptreload"
	"github.com/Tencent/WeKnora/internal/custom/service/transcript"
)

type scriptedMeetingLLM struct {
	outputs []string
	calls   int
	inputs  []string
}

type flakyMeetingLLM struct {
	calls  int
	output string
}

type meetingEvidenceReader struct{}

func (meetingEvidenceReader) Read(context.Context, string, string) ([]transcript.Chunk, error) {
	return []transcript.Chunk{{EvidenceSentenceID: "e1", Content: "客户权限审批流进入执行阶段。", StartMs: 1000, EndMs: 2500}}, nil
}

func (s *scriptedMeetingLLM) CompleteJSONWithSystem(_ context.Context, _, input string) (string, error) {
	s.inputs = append(s.inputs, input)
	value := s.outputs[s.calls]
	s.calls++
	return value, nil
}

type staticMeetingSummaryReader struct{ page *weknora.WikiPage }

func (s staticMeetingSummaryReader) GetPageByID(context.Context, string, string) (*weknora.WikiPage, error) {
	return s.page, nil
}

func (s *flakyMeetingLLM) CompleteJSONWithSystem(context.Context, string, string) (string, error) {
	s.calls++
	if s.calls == 1 {
		return "", errors.New("temporary provider connection failure")
	}
	return s.output, nil
}

func TestAIProjectionMapsUpdatesToSeekableFacts(t *testing.T) {
	llm := &scriptedMeetingLLM{outputs: []string{
		`{"candidates":[{"candidate_refs":["section-1"],"business_object":{"name":"客户项目","scope":"客户项目","aliases":[],"evidence_ids":["e1"]},"specific_question":"权限审批流改造","work_item_scope":null,"work_item_scope_evidence_ids":[],"cycle_signal":null,"cycle_signal_evidence_ids":[],"core_level":"core","reason":"会议明确进入执行","evidence_ids":["e1"]}]}`,
		`{"matches":[{"candidate_id":"candidate-001","object_decision":"new_object","matched_topic_id":null,"reason":"新对象","candidate_evidence_ids":["e1"],"matched_topic_evidence_ids":[]}]}`,
		`{"matches":[{"candidate_id":"candidate-001","work_item_decision":"new_item","matched_work_item_id":null,"reason":"新事项","candidate_evidence_ids":["e1"],"matched_work_item_evidence_ids":[]}]}`,
		`{"work_item_updates":[{"candidate_id":"candidate-001","topic_id":"topic-01","work_item_ref":"item-01-01","current_status":"in_progress","current_status_evidence_ids":["e1"],"current_conclusion":"按现有方案进入执行","current_conclusion_evidence_ids":["e1"],"change":{"is_substantive":true,"change_type":"adjusted","discussion":"确认审批流改造进入执行","conclusion":"按现有方案进入执行","change_from_previous":"从评审转为执行","current_progress":"执行中","evidence_ids":["e1"],"previous_evidence_ids":["e1"],"current_evidence_ids":["e1"]},"important_decisions":[{"content":"确认按现有方案执行","decision_type":"confirmed","replaces_decision_id":null,"evidence_ids":["e1"]}],"decision_effect_updates":[],"todos":[{"todo_match_decision":"new_todo","matched_todo_id":null,"todo_type":"action","content":"完成审批流改造开发","owner":null,"due_text":null,"priority":null,"todo_status_suggestion":null,"status_evidence_ids":[],"evidence_ids":["e1"]}]}]}`,
		`{"knowledge_refs":[]}`, `{"relations":[]}`,
	}}
	bundle := promptreload.New("", map[string]string{"meeting-item-candidate-v1.txt": "candidate", "meeting-topic-cluster-matching-v1.txt": "topic", "meeting-work-item-matching-v1.txt": "item", "meeting-work-item-update-v1.txt": "update", "meeting-knowledge-selection-v1.txt": "knowledge", "meeting-topic-relation-v1.txt": "relation"})
	snapshot, err := bundle.Load()
	if err != nil {
		t.Fatal(err)
	}
	projection, err := (&AIProjectionGenerator{LLM: llm, EvidenceReader: meetingEvidenceReader{}}).Generate(context.Background(), []model.Video{{ID: "v1", Title: "客户项目会议", TranscriptGeneration: "g1"}}, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	cluster := projection.TopicClusters[0]
	if cluster.WorkItems[0].Status != "in_progress" || len(cluster.WorkItems[0].EvidenceRefs) != 1 {
		t.Fatalf("work item was not mapped: %+v", cluster.WorkItems[0])
	}
	if len(cluster.Decisions) != 1 || cluster.Decisions[0].VideoID != "v1" || len(cluster.Decisions[0].EvidenceRefs) != 1 {
		t.Fatalf("decision was not mapped: %+v", cluster.Decisions)
	}
	if len(cluster.Todos) != 1 || cluster.Todos[0].VideoID != "v1" || len(cluster.Todos[0].EvidenceRefs) != 1 {
		t.Fatalf("todo was not mapped: %+v", cluster.Todos)
	}
	if len(cluster.Evolution) != 2 || cluster.Evolution[1].VideoID != "v1" {
		t.Fatalf("evolution was not mapped: %+v", cluster.Evolution)
	}
}

func TestAIProjectionGeneratorRunsAllPromptStages(t *testing.T) {
	llm := &scriptedMeetingLLM{outputs: []string{
		`{"candidates":[{"candidate_refs":["section-1"],"business_object":{"name":"客户","scope":"客户项目","aliases":[],"evidence_ids":["e1"]},"specific_question":"权限审批流改造","work_item_scope":null,"work_item_scope_evidence_ids":[],"cycle_signal":null,"cycle_signal_evidence_ids":[],"core_level":"core","reason":"会议明确推进事项","evidence_ids":["e1"]}]}`,
		`{"matches":[{"candidate_id":"candidate-001","object_decision":"new_object","matched_topic_id":null,"reason":"新对象","candidate_evidence_ids":["e1"],"matched_topic_evidence_ids":[]}]}`,
		`{"matches":[{"candidate_id":"candidate-001","work_item_decision":"new_item","matched_work_item_id":null,"reason":"新事项","candidate_evidence_ids":["e1"],"matched_work_item_evidence_ids":[]}]}`,
		`{"work_item_updates":[{"candidate_id":"candidate-001","topic_id":"topic-01","work_item_ref":"item-01-01","current_status":null,"current_status_evidence_ids":[],"current_conclusion":null,"current_conclusion_evidence_ids":[],"change":{"is_substantive":false,"change_type":null,"discussion":null,"conclusion":null,"change_from_previous":null,"current_progress":null,"evidence_ids":[],"previous_evidence_ids":[],"current_evidence_ids":[]},"important_decisions":[],"decision_effect_updates":[],"todos":[]}]}`,
		`{"knowledge_refs":[]}`, `{"relations":[]}`,
	}}
	bundle := promptreload.New("", map[string]string{"meeting-item-candidate-v1.txt": "candidate", "meeting-topic-cluster-matching-v1.txt": "topic", "meeting-work-item-matching-v1.txt": "item", "meeting-work-item-update-v1.txt": "update", "meeting-knowledge-selection-v1.txt": "knowledge", "meeting-topic-relation-v1.txt": "relation"})
	snapshot, err := bundle.Load()
	if err != nil {
		t.Fatal(err)
	}
	generated, err := (&AIProjectionGenerator{LLM: llm, EvidenceReader: meetingEvidenceReader{}}).Generate(context.Background(), []model.Video{{ID: "v1", Title: "客户权限审批流改造", TranscriptGeneration: "g1"}}, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if llm.calls != 6 {
		t.Fatalf("calls = %d, want 6", llm.calls)
	}
	if len(generated.TopicClusters) != 1 {
		t.Fatalf("clusters = %d", len(generated.TopicClusters))
	}
}

func TestAIProjectionCandidateInputIncludesSummarySemanticsAndEvidence(t *testing.T) {
	llm := &scriptedMeetingLLM{outputs: []string{`{"candidates":[]}`, `{"relations":[]}`}}
	summaryPage := &weknora.WikiPage{Content: `{"schemaVersion":2,"videoType":"meeting","orchestrationProfile":{"schemaVersion":1,"primaryTopic":"客户项目推进","topicUnits":[{"title":"审批流改造","abstract":"审批流进入执行阶段","contentForms":["process_standard"],"learningOutcomes":["完成改造"],"summaryBlockIds":["block-1"],"evidenceChunkIds":["chunk-1"],"evidenceRefs":[{"evidence_sentence_id":"e1","start_ms":1000,"end_ms":2500}]}]},"sections":[{"id":"discussion-details","title":"讨论详情","blocks":[{"id":"block-1","kind":"paragraph","text":"完成客户权限审批流改造","evidenceChunkIds":["chunk-1"],"knowledge_refs":[],"evidence_refs":[{"evidence_sentence_id":"e1","start_ms":1000,"end_ms":2500}],"evidence":[]}]}]}`}
	snapshot, err := NewPromptBundle("").Load()
	if err != nil {
		t.Fatal(err)
	}
	_, err = (&AIProjectionGenerator{LLM: llm, EvidenceReader: meetingEvidenceReader{}, SummaryReader: staticMeetingSummaryReader{page: summaryPage}, KnowledgeBaseID: "knowledge"}).Generate(context.Background(), []model.Video{{ID: "v1", Title: "客户项目会议", SummaryWikiPageID: "summary-1", TranscriptGeneration: "g1"}}, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(llm.inputs) == 0 {
		t.Fatal("candidate stage was not called")
	}
	for _, expected := range []string{"客户项目推进", "审批流进入执行阶段", "完成客户权限审批流改造", `"evidence_ids":["e1"]`, `"allowed_section_refs":["discussion-details"]`} {
		if !strings.Contains(llm.inputs[0], expected) {
			t.Fatalf("candidate input missing %q: %s", expected, llm.inputs[0])
		}
	}
}

func TestValidateItemCandidatesRejectsEmptyOutputForActionableSummary(t *testing.T) {
	if err := validateItemCandidates(ItemCandidateOutput{Candidates: []ItemCandidate{}}, map[string]struct{}{}, map[string]struct{}{}, true); err == nil {
		t.Fatal("evidence-backed actionable summary accepted empty candidates")
	}
	if err := validateItemCandidates(ItemCandidateOutput{Candidates: []ItemCandidate{}}, map[string]struct{}{}, map[string]struct{}{}, false); err != nil {
		t.Fatalf("non-actionable summary must allow empty candidates: %v", err)
	}
}

func TestValidateItemCandidatesRejectsSummaryBlockReference(t *testing.T) {
	output := ItemCandidateOutput{Candidates: []ItemCandidate{{
		CandidateRefs:    []string{"block-action-items-1"},
		BusinessObject:   BusinessObject{Name: "客户项目", Scope: "客户项目", Aliases: []string{}, EvidenceIDs: []string{"e1"}},
		SpecificQuestion: "权限审批流改造", CoreLevel: "core", Reason: "会议明确推进", EvidenceIDs: []string{"e1"},
	}}}
	err := validateItemCandidates(output, map[string]struct{}{"e1": {}}, map[string]struct{}{"action-items": {}}, true)
	if err == nil || !strings.Contains(err.Error(), "not in whitelist") {
		t.Fatalf("summary block reference was not rejected: %v", err)
	}
}

func TestActionableMeetingSectionsCoverCurrentAndLegacyContracts(t *testing.T) {
	for _, sectionID := range []string{"action-items", "deferred-topics", "important-decisions", "differences-pending-decisions", "actions-next-steps"} {
		if !isActionableMeetingSection(sectionID) {
			t.Fatalf("section %q must require an item candidate", sectionID)
		}
	}
	for _, sectionID := range []string{"meeting-summary", "meeting-basic-information", "discussion-details", "other"} {
		if isActionableMeetingSection(sectionID) {
			t.Fatalf("section %q must not force an item candidate", sectionID)
		}
	}
}

func TestAIProjectionDoesNotPublishPossibleMatches(t *testing.T) {
	llm := &scriptedMeetingLLM{outputs: []string{
		`{"candidates":[{"candidate_refs":["section-1"],"business_object":{"name":"客户项目","scope":"客户项目","aliases":[],"evidence_ids":["e1"]},"specific_question":"权限审批流改造","work_item_scope":null,"work_item_scope_evidence_ids":[],"cycle_signal":null,"cycle_signal_evidence_ids":[],"core_level":"core","reason":"证据充分","evidence_ids":["e1"]}]}`,
		`{"matches":[{"candidate_id":"candidate-001","object_decision":"possible","matched_topic_id":null,"reason":"对象边界未确认","candidate_evidence_ids":["e1"],"matched_topic_evidence_ids":[]}]}`,
		`{"relations":[]}`,
	}}
	snapshot, err := NewPromptBundle("").Load()
	if err != nil {
		t.Fatal(err)
	}
	generated, err := (&AIProjectionGenerator{LLM: llm, EvidenceReader: meetingEvidenceReader{}}).Generate(context.Background(), []model.Video{{ID: "v1", Title: "客户项目会议", TranscriptGeneration: "g1"}}, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(generated.TopicClusters) != 0 {
		t.Fatalf("possible object match created formal clusters: %#v", generated.TopicClusters)
	}
}

func TestAIProjectionCorrectsOneInvalidStageOutput(t *testing.T) {
	llm := &scriptedMeetingLLM{outputs: []string{
		`{"candidates":[]} trailing`,
		`{"candidates":[]}`,
	}}
	snapshot, err := NewPromptBundle("").Load()
	if err != nil {
		t.Fatal(err)
	}
	var output ItemCandidateOutput
	if _, err := (&AIProjectionGenerator{LLM: llm}).callJSONValidated(context.Background(), snapshot, "meeting-item-candidate-v1.txt", map[string]any{"videos": []any{}}, func(raw string) error {
		if err := decodeStrict(raw, &output); err != nil {
			return err
		}
		return output.Validate(map[string]struct{}{})
	}); err != nil {
		t.Fatal(err)
	}
	if llm.calls != 2 || len(output.Candidates) != 0 {
		t.Fatalf("expected one correction call, calls=%d output=%#v", llm.calls, output)
	}
}

func TestAIProjectionStopsAfterOneCorrectionFailure(t *testing.T) {
	llm := &scriptedMeetingLLM{outputs: []string{`{"candidates":[]} trailing`, `{"candidates":[]} trailing`}}
	snapshot, err := NewPromptBundle("").Load()
	if err != nil {
		t.Fatal(err)
	}
	var output ItemCandidateOutput
	_, err = (&AIProjectionGenerator{LLM: llm}).callJSONValidated(context.Background(), snapshot, "meeting-item-candidate-v1.txt", map[string]any{"videos": []any{}}, func(raw string) error {
		if err := decodeStrict(raw, &output); err != nil {
			return err
		}
		return output.Validate(map[string]struct{}{})
	})
	var generationErr *GenerationError
	if !errors.As(err, &generationErr) || generationErr.Code != "model_correction_failed" || llm.calls != 2 {
		t.Fatalf("expected bounded correction failure, calls=%d err=%v", llm.calls, err)
	}
}

func TestAIProjectionRetriesTransportOnceWithinStageBudget(t *testing.T) {
	llm := &flakyMeetingLLM{output: `{"candidates":[]}`}
	snapshot, err := NewPromptBundle("").Load()
	if err != nil {
		t.Fatal(err)
	}
	var output ItemCandidateOutput
	if _, err := (&AIProjectionGenerator{LLM: llm}).callJSONValidated(context.Background(), snapshot, "meeting-item-candidate-v1.txt", map[string]any{"videos": []any{}}, func(raw string) error {
		if err := decodeStrict(raw, &output); err != nil {
			return err
		}
		return output.Validate(map[string]struct{}{})
	}); err != nil {
		t.Fatal(err)
	}
	if llm.calls != 2 || len(output.Candidates) != 0 {
		t.Fatalf("expected one transport retry, calls=%d output=%#v", llm.calls, output)
	}
}

func TestNormalizeTopicRelationTypeIsExplicit(t *testing.T) {
	for _, value := range []string{RelationPrerequisite, RelationConflictConstraint, RelationSharedSupport, RelationResultFeedback} {
		got, err := normalizeTopicRelationType(value)
		if err != nil || got != value {
			t.Fatalf("relation type %q mapped to %q, err=%v", value, got, err)
		}
	}
	if _, err := normalizeTopicRelationType("supports"); err == nil {
		t.Fatal("knowledge relation type must not enter the meeting topic relation contract")
	}
}
